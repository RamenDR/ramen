// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

// drpc_networkmapping.go — network-mapping ConfigMap parsing and IP translation
// for VM static-IP DR.
//
// # ConfigMap contract
//
// The DRPC carries an annotation:
//
//	drplacementcontrol.ramendr.openshift.io/network-mapping: "<configmap-name>"
//
// That annotation references a ConfigMap in the same namespace as the DRPC.
// The ConfigMap's "mappings.yaml" data key contains a YAML document.
// Cluster names (dr1/dr2) come from drpolicy.spec.drClusters[0] and [1].
//
// # Schema — each block maps one NAD and may carry any combination of rules
//
//  version: "v1"    # required; must be "v1" for this release
//	networkMappings:
//
//	  - networkRef:
//	      nadNamespace: default
//	      nadName:      backup
//
//	    # CIDR translation — bidirectional subnet-offset mapping.
//	    # Presence is detected from dr1/dr2 keys.
//	    cidr:
//	      dr1: 192.168.100.0/24
//	      dr2: 192.168.110.0/24
//
//	    # Explicit per-VM overrides — checked before CIDR.
//	    explicitMappings:
//	      - dr1: 192.168.100.10
//	        dr2: 192.168.110.10
//	      - dr1: 192.168.100.51
//	        dr2: 192.168.110.58
//
//	    # Regex fallback — direction is required because patterns are not
//	    # inherently reversible.
//	    regexMappings:
//	      dr1-to-dr2:
//	        - pattern:     "^172\\.16\\.100\\.(\\d+)$"
//	          replacement: "10.20.30.$1"
//	      dr2-to-dr1:
//	        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
//	          replacement: "172.16.100.$1"
//
// # Evaluation order (per discovered IP)
//
// Given (networkRef, sourceCluster, ip):
//  1. explicitMappings — most specific; exact IP match wins immediately.
//  2. cidr             — subnet-offset translation.
//  3. regexMappings    — fallback regex rules; first matching pattern wins.
//
// At least one of the three sections must be present per block.
//
// # Direction model
//
// "dr1" = drpolicy.spec.drClusters[0], "dr2" = drpolicy.spec.drClusters[1].
// Forward direction = dr1 → dr2 (failover).
// Reverse direction = dr2 → dr1 (failback).
// CIDR and explicitMappings are bidirectional (dr1/dr2 keys supply both sides).
// regexMappings direction must be stated explicitly in the YAML keys.
//
// # Versioning
//
// The top-level "version" field allows the schema to evolve across releases
// while keeping backward compatibility.  ParseNetworkMappingConfigMap rejects
// any version it does not recognize so that an operator upgraded to a future
// release can detect stale ConfigMaps rather than misparse them silently.
//
// Current supported version: "v1"
import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"regexp"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// TranslationDirection controls which side (Forward = dr1→dr2, Reverse = dr2→dr1)
// of a mapping block is used.
type TranslationDirection int

const (
	TranslationDirectionForward TranslationDirection = iota // dr1 → dr2
	TranslationDirectionReverse                             // dr2 → dr1
)

// RuleType identifies which rule produced a translation result (used in logs).
type RuleType string

const (
	RuleTypeExplicit RuleType = "Explicit"
	RuleTypeCIDR     RuleType = "CIDR"
	RuleTypeRegex    RuleType = "Regex"
)

const DRClusterPairCount = 2

// ---------------------------------------------------------------------------
// Version constants
// ---------------------------------------------------------------------------

// networkMappingSchemaV1 is the only version accepted by this release.
// Future releases may add new values and handle old ones with migration logic.
const networkMappingSchemaV1 = "v1"

// ---------------------------------------------------------------------------
// On-disk (raw) types — decoded directly from mappings.yaml
// ---------------------------------------------------------------------------

// rawNetworkMappingConfig is the top-level structure decoded from mappings.yaml.
type rawNetworkMappingConfig struct {
	// Version identifies the schema version of this document.  Required.
	Version         string              `yaml:"version"`
	NetworkMappings []rawNetworkMapping `yaml:"networkMappings"`
}

// rawNetworkMapping is one entry in the networkMappings list.
type rawNetworkMapping struct {
	// NetworkRef identifies the NAD this block applies to.
	NetworkRef NetworkRef `yaml:"networkRef"`

	// CIDR holds the subnet CIDRs keyed by cluster name.  Both dr1 and dr2
	// keys must be present when this section is used.
	// +optional
	CIDR map[string]string `yaml:"cidr,omitempty"`

	// ExplicitMappings holds per-VM static IP pairs keyed by cluster name.
	// +optional
	ExplicitMappings []rawClusterIPPair `yaml:"explicitMappings,omitempty"`

	// RegexMappings holds ordered regex rules keyed by direction string
	// "<dr1>-to-<dr2>" and "<dr2>-to-<dr1>".
	// +optional
	RegexMappings map[string][]rawRegexRule `yaml:"regexMappings,omitempty"`
}

// rawClusterIPPair holds one row of an explicit mapping keyed by cluster name.
type rawClusterIPPair map[string]string

// rawRegexRule is a single pattern/replacement pair inside regexMappings.
type rawRegexRule struct {
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"`
}

// ---------------------------------------------------------------------------
// Runtime types — produced after cluster-name resolution and validation
// ---------------------------------------------------------------------------

// NetworkMappingRules is the normalised, direction-resolved form of the full
// ConfigMap.
type NetworkMappingRules struct {
	// Version is the schema version read from the ConfigMap (e.g. "v1").
	Version string
	// DR1 is drpolicy.spec.drClusters[0].
	DR1 string
	// DR2 is drpolicy.spec.drClusters[1].
	DR2 string

	// Blocks is the ordered list of resolved per-NAD translation blocks.
	Blocks []NetworkMappingBlock
}

// NetworkRef identifies the NAD (NetworkAttachmentDefinition) that a block
// applies to.
type NetworkRef struct {
	NADNamespace string `yaml:"nadNamespace" json:"nadNamespace"`
	NADName      string `yaml:"nadName"      json:"nadName"`
}

// NetworkMappingBlock is one resolved per-NAD translation block.
// All three rule sets are optional individually; at least one must be present.
// Evaluation order at runtime: ExplicitMappings → CIDRMapping → RegexRules.
type NetworkMappingBlock struct {
	// NetworkRef scopes this block to a specific NAD.
	NetworkRef NetworkRef

	// ExplicitMappings holds the per-VM static IP table (highest priority).
	// Forward maps a dr1 IP to its dr2 IP; Reverse is the inverse.
	// Both maps are nil when explicitMappings is not configured.
	ExplicitMappings *ExplicitMappings

	// CIDRMapping is the subnet-offset translation (second priority).
	// nil when not configured.
	CIDRMapping *CIDRMapping

	// RegexRules is the directional regex fallback (lowest priority).
	// nil when not configured.
	RegexRules *RegexRules
}

// ExplicitMappings holds the forward and reverse lookup maps built from the
// explicitMappings YAML list.  Both maps are populated together at parse time
// so lookup is O(1) in both directions.
//
// Forward: dr1IP → dr2IP (failover, dr1→dr2)
// Reverse: dr2IP → dr1IP (failback, dr2→dr1)
type ExplicitMappings struct {
	Forward map[string]string // dr1IP → dr2IP
	Reverse map[string]string // dr2IP → dr1IP
}

// CIDRMapping carries the two subnets for subnet-offset translation.
// Both subnets must have the same prefix length.
type CIDRMapping struct {
	// DR1CIDR is the subnet on cluster dr1.
	DR1CIDR string
	// DR2CIDR is the subnet on cluster dr2.
	DR2CIDR string

	// pre-parsed networks (set at validation time to avoid repeated parsing).
	dr1Net *net.IPNet
	dr2Net *net.IPNet
}

// RegexRules holds the two ordered direction lists for regex-based translation.
type RegexRules struct {
	// Forward rules applied during dr1→dr2.
	Forward []CompiledRegexRule
	// Reverse rules applied during dr2→dr1.
	Reverse []CompiledRegexRule
}

// CompiledRegexRule is a single pre-compiled regex-replacement rule.
type CompiledRegexRule struct {
	compiled    *regexp.Regexp
	Pattern     string // original pattern string, kept for error messages
	Replacement string
}

// ---------------------------------------------------------------------------
// DRPCNetworkMappingManager
// ---------------------------------------------------------------------------

// DRPCNetworkMappingManager reads and parses the network-mapping ConfigMap
// referenced by a DRPC annotation and performs IP translation.
type DRPCNetworkMappingManager struct {
	client client.Client
	log    logr.Logger
}

// NewDRPCNetworkMappingManager constructs a manager bound to the given client.
func NewDRPCNetworkMappingManager(c client.Client, log logr.Logger) *DRPCNetworkMappingManager {
	return &DRPCNetworkMappingManager{client: c, log: log}
}

// LoadNetworkMapping returns the parsed NetworkMappingRules for the DRPC, or
// (nil, nil) if the DRPC does not carry the annotation.
//
// drPolicy is required to resolve cluster names from drpolicy.spec.drClusters.
func (m *DRPCNetworkMappingManager) LoadNetworkMapping(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
	drPolicy *rmn.DRPolicy,
) (*NetworkMappingRules, error) {
	var cmName string
	// 1. DRPolicy.Spec.NetworkMappingRef, set by cluster admin once
	if isNetworkMappingEnabled(drPolicy) {
		cmName = drPolicy.Spec.NetworkMappingRef.Name

		m.log.Info("Using network-mapping ConfigMap from DRPolicy")
	}

	if len(cmName) == 0 {
		m.log.V(1).Info("DRPC has no network-mapping annotation; skipping",
			"drpc", drpc.Name)

		return nil, nil
	}

	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: cmName, Namespace: drpc.Namespace}

	if err := m.client.Get(ctx, key, cm); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("network-mapping ConfigMap %q not found in namespace %q",
				cmName, drpc.Namespace)
		}

		return nil, fmt.Errorf("failed to get network-mapping ConfigMap %q: %w", cmName, err)
	}

	if len(drPolicy.Spec.DRClusters) != DRClusterPairCount {
		return nil, fmt.Errorf("DRPolicy %q must have exactly 2 drClusters, got %d",
			drPolicy.Name, len(drPolicy.Spec.DRClusters))
	}

	dr1, dr2 := drPolicy.Spec.DRClusters[0], drPolicy.Spec.DRClusters[1]

	rules, err := ParseNetworkMappingConfigMap(cm, dr1, dr2)
	if err != nil {
		return nil, fmt.Errorf("failed to parse network-mapping ConfigMap %q: %w", cmName, err)
	}

	m.log.Info("Loaded network-mapping ConfigMap",
		"configmap", cmName,
		"dr1", dr1,
		"dr2", dr2,
		"blocks", len(rules.Blocks))

	return rules, nil
}

// ---------------------------------------------------------------------------
// ParseNetworkMappingConfigMap
// ---------------------------------------------------------------------------

// ParseNetworkMappingConfigMap parses a ConfigMap that follows the
// network-mapping format described at the top of this file.
//
// dr1 = drpolicy.spec.drClusters[0], dr2 = drpolicy.spec.drClusters[1].
func ParseNetworkMappingConfigMap(cm *corev1.ConfigMap, dr1, dr2 string) (*NetworkMappingRules, error) {
	raw, ok := cm.Data[networkMappingDataKey]
	if !ok {
		return nil, fmt.Errorf("ConfigMap %q/%q has no %q data key",
			cm.Namespace, cm.Name, networkMappingDataKey)
	}

	if dr1 == "" || dr2 == "" {
		return nil, fmt.Errorf("dr1 and dr2 cluster names must be non-empty")
	}

	if dr1 == dr2 {
		return nil, fmt.Errorf("dr1 and dr2 cluster names must be distinct, got %q", dr1)
	}

	rawCfg := &rawNetworkMappingConfig{}
	if err := sigsyaml.Unmarshal([]byte(raw), rawCfg); err != nil {
		return nil, fmt.Errorf("failed to parse %q in ConfigMap %q: %w",
			networkMappingDataKey, cm.Name, err)
	}

	if rawCfg.Version == "" {
		return nil, fmt.Errorf("ConfigMap %q: %q is missing required field \"version\" (expected %q)",
			cm.Name, networkMappingDataKey, networkMappingSchemaV1)
	}

	if rawCfg.Version != networkMappingSchemaV1 {
		return nil, fmt.Errorf("ConfigMap %q: unsupported network-mapping schema version %q (supported: %q)",
			cm.Name, rawCfg.Version, networkMappingSchemaV1)
	}

	rules, err := resolveAndValidate(rawCfg, dr1, dr2)
	if err != nil {
		return nil, fmt.Errorf("invalid network mapping in ConfigMap %q: %w", cm.Name, err)
	}

	return rules, nil
}

// ---------------------------------------------------------------------------
// resolveAndValidate — raw → runtime with full validation
// ---------------------------------------------------------------------------

func resolveAndValidate(rawCfg *rawNetworkMappingConfig, dr1, dr2 string) (*NetworkMappingRules, error) {
	if len(rawCfg.NetworkMappings) == 0 {
		return nil, fmt.Errorf("networkMappings list must not be empty")
	}

	fwdKey := dr1 + "-to-" + dr2
	revKey := dr2 + "-to-" + dr1

	rules := &NetworkMappingRules{
		Version: rawCfg.Version,
		DR1:     dr1,
		DR2:     dr2,
		Blocks:  make([]NetworkMappingBlock, 0, len(rawCfg.NetworkMappings)),
	}

	seen := make(map[string]int, len(rawCfg.NetworkMappings))

	for i, raw := range rawCfg.NetworkMappings {
		if raw.NetworkRef.NADNamespace == "" {
			return nil, fmt.Errorf("networkMappings[%d]: networkRef.nadNamespace is required", i)
		}

		if raw.NetworkRef.NADName == "" {
			return nil, fmt.Errorf("networkMappings[%d]: networkRef.nadName is required", i)
		}

		nadKey := raw.NetworkRef.NADNamespace + "/" + raw.NetworkRef.NADName
		if prev, dup := seen[nadKey]; dup {
			return nil, fmt.Errorf("networkMappings[%d]: duplicate networkRef %s (already at index %d)",
				i, nadKey, prev)
		}

		seen[nadKey] = i

		block, err := resolveBlock(i, raw, dr1, dr2, fwdKey, revKey)
		if err != nil {
			return nil, err
		}

		rules.Blocks = append(rules.Blocks, block)
	}

	return rules, nil
}

// resolveBlock resolves and validates a single rawNetworkMapping into a
// NetworkMappingBlock.
func resolveBlock(
	blockIdx int,
	raw rawNetworkMapping,
	dr1, dr2, fwdKey, revKey string,
) (NetworkMappingBlock, error) {
	block := NetworkMappingBlock{NetworkRef: raw.NetworkRef}

	// --- explicitMappings ---
	if len(raw.ExplicitMappings) > 0 {
		em, err := resolveExplicitMappings(blockIdx, raw.ExplicitMappings, dr1, dr2)
		if err != nil {
			return block, err
		}

		block.ExplicitMappings = em // *ExplicitMappings
	}

	// --- cidr ---
	if len(raw.CIDR) > 0 {
		cm, err := resolveCIDR(blockIdx, raw.CIDR, dr1, dr2)
		if err != nil {
			return block, err
		}

		block.CIDRMapping = cm
	}

	// --- regexMappings ---
	if len(raw.RegexMappings) > 0 {
		rr, err := resolveRegexMappings(blockIdx, raw.RegexMappings, fwdKey, revKey)
		if err != nil {
			return block, err
		}

		block.RegexRules = rr
	}

	// At least one rule set must be present.
	if block.ExplicitMappings == nil && block.CIDRMapping == nil && block.RegexRules == nil {
		return block, fmt.Errorf(
			"networkMappings[%d] (%s/%s): at least one of cidr, explicitMappings, or regexMappings is required",
			blockIdx, raw.NetworkRef.NADNamespace, raw.NetworkRef.NADName)
	}

	return block, nil
}

// ---------------------------------------------------------------------------
// explicitMappings resolver
// ---------------------------------------------------------------------------

func resolveExplicitMappings(
	blockIdx int,
	rawPairs []rawClusterIPPair,
	dr1, dr2 string,
) (*ExplicitMappings, error) {
	fwd := make(map[string]string, len(rawPairs))
	rev := make(map[string]string, len(rawPairs))

	for j, pair := range rawPairs {
		dr1IP, dr2IP, err := extractClusterIPs(
			blockIdx, j, pair, dr1, dr2,
		)
		if err != nil {
			return nil, err
		}

		dr1Norm, dr2Norm, err := normalizeExplicitIPs(
			blockIdx, j, dr1, dr2, dr1IP, dr2IP,
		)
		if err != nil {
			return nil, err
		}

		if err := validateExplicitMappingCollision(
			blockIdx, j,
			dr1Norm, dr2Norm,
			dr1, dr2,
			fwd, rev,
		); err != nil {
			return nil, err
		}

		fwd[dr1Norm] = dr2Norm
		rev[dr2Norm] = dr1Norm
	}

	return &ExplicitMappings{
		Forward: fwd,
		Reverse: rev,
	}, nil
}

// ---------------------------------------------------------------------------
// CIDR resolver
// ---------------------------------------------------------------------------

func resolveCIDR(
	blockIdx int,
	rawCIDR map[string]string,
	dr1, dr2 string,
) (*CIDRMapping, error) {
	dr1CIDR, dr2CIDR, err := validateCIDRKeys(blockIdx, rawCIDR, dr1, dr2)
	if err != nil {
		return nil, err
	}

	dr1Net, dr2Net, err := parseAndValidateCIDRs(
		blockIdx,
		dr1, dr2,
		dr1CIDR, dr2CIDR,
	)
	if err != nil {
		return nil, err
	}

	return &CIDRMapping{
		DR1CIDR: dr1CIDR,
		DR2CIDR: dr2CIDR,
		dr1Net:  dr1Net,
		dr2Net:  dr2Net,
	}, nil
}

func validateCIDRKeys(
	blockIdx int,
	rawCIDR map[string]string,
	dr1, dr2 string,
) (string, string, error) {
	dr1CIDR, hasDR1 := rawCIDR[dr1]
	if !hasDR1 {
		return "", "", fmt.Errorf("networkMappings[%d]: cidr: missing cluster key %q", blockIdx, dr1)
	}

	dr2CIDR, hasDR2 := rawCIDR[dr2]
	if !hasDR2 {
		return "", "", fmt.Errorf("networkMappings[%d]: cidr: missing cluster key %q", blockIdx, dr2)
	}

	for k := range rawCIDR {
		if k != dr1 && k != dr2 {
			return "", "", fmt.Errorf("networkMappings[%d]: cidr: unrecognized key %q (expected %q or %q)",
				blockIdx, k, dr1, dr2)
		}
	}

	return dr1CIDR, dr2CIDR, nil
}

func parseAndValidateCIDRs(
	blockIdx int,
	dr1, dr2 string,
	dr1CIDR, dr2CIDR string,
) (*net.IPNet, *net.IPNet, error) {
	_, dr1Net, err := net.ParseCIDR(dr1CIDR)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"networkMappings[%d]: cidr.%s: invalid CIDR %q: %w",
			blockIdx, dr1, dr1CIDR, err,
		)
	}

	_, dr2Net, err := net.ParseCIDR(dr2CIDR)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"networkMappings[%d]: cidr.%s: invalid CIDR %q: %w",
			blockIdx, dr2, dr2CIDR, err,
		)
	}

	if err := validateCIDRNetworks(
		blockIdx,
		dr1, dr2,
		dr1CIDR, dr2CIDR,
		dr1Net, dr2Net,
	); err != nil {
		return nil, nil, err
	}

	return dr1Net, dr2Net, nil
}

func validateCIDRNetworks(
	blockIdx int,
	dr1, dr2 string,
	dr1CIDR, dr2CIDR string,
	dr1Net, dr2Net *net.IPNet,
) error {
	dr1Ones, dr1Bits := dr1Net.Mask.Size()
	dr2Ones, dr2Bits := dr2Net.Mask.Size()

	if dr1Bits != dr2Bits {
		return fmt.Errorf(
			"networkMappings[%d]: cidr: %s and %s CIDRs have different address families",
			blockIdx, dr1, dr2,
		)
	}

	if dr1Ones != dr2Ones {
		return fmt.Errorf(
			"networkMappings[%d]: cidr: %s (/%d) and %s (/%d) have different prefix lengths; equal prefix lengths are required",
			blockIdx, dr1, dr1Ones, dr2, dr2Ones,
		)
	}

	if dr1Net.IP.To4() == nil || dr2Net.IP.To4() == nil {
		return fmt.Errorf(
			"networkMappings[%d]: cidr: only IPv4 CIDRs are supported (%s=%q, %s=%q)",
			blockIdx, dr1, dr1CIDR, dr2, dr2CIDR,
		)
	}

	if dr1Net.IP.Equal(dr2Net.IP) {
		return fmt.Errorf(
			"networkMappings[%d]: cidr: %s and %s are the same network (%s); source and destination subnets must differ",
			blockIdx, dr1, dr2, dr1Net.String(),
		)
	}

	return nil
}

// ---------------------------------------------------------------------------
// regexMappings resolver
// ---------------------------------------------------------------------------

func resolveRegexMappings(
	blockIdx int,
	rawRegex map[string][]rawRegexRule,
	fwdKey, revKey string,
) (*RegexRules, error) {
	for k := range rawRegex {
		if k != fwdKey && k != revKey {
			return nil, fmt.Errorf(
				"networkMappings[%d]: regexMappings: unrecognized direction key %q (expected %q or %q)",
				blockIdx, k, fwdKey, revKey)
		}
	}

	fwdRaw, hasFwd := rawRegex[fwdKey]
	revRaw, hasRev := rawRegex[revKey]

	if !hasFwd || len(fwdRaw) == 0 {
		return nil, fmt.Errorf(
			"networkMappings[%d]: regexMappings[%q] is required and must have at least one rule",
			blockIdx, fwdKey)
	}

	if !hasRev || len(revRaw) == 0 {
		return nil, fmt.Errorf(
			"networkMappings[%d]: regexMappings[%q] is required and must have at least one rule",
			blockIdx, revKey)
	}

	fwd, err := compileRegexRules(blockIdx, fwdKey, fwdRaw)
	if err != nil {
		return nil, err
	}

	rev, err := compileRegexRules(blockIdx, revKey, revRaw)
	if err != nil {
		return nil, err
	}

	return &RegexRules{Forward: fwd, Reverse: rev}, nil
}

func compileRegexRules(blockIdx int, dirKey string, raw []rawRegexRule) ([]CompiledRegexRule, error) {
	rules := make([]CompiledRegexRule, 0, len(raw))

	for j, r := range raw {
		if r.Pattern == "" {
			return nil, fmt.Errorf("networkMappings[%d]: regexMappings[%q][%d]: pattern is required",
				blockIdx, dirKey, j)
		}

		if r.Replacement == "" {
			return nil, fmt.Errorf("networkMappings[%d]: regexMappings[%q][%d]: replacement is required",
				blockIdx, dirKey, j)
		}

		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("networkMappings[%d]: regexMappings[%q][%d]: invalid pattern %q: %w",
				blockIdx, dirKey, j, r.Pattern, err)
		}

		rules = append(rules, CompiledRegexRule{compiled: re, Pattern: r.Pattern, Replacement: r.Replacement})
	}

	return rules, nil
}

// ---------------------------------------------------------------------------
// Runtime lookup
// ---------------------------------------------------------------------------

// findBlock returns the NetworkMappingBlock for the given NAD, or nil.
func findBlock(rules *NetworkMappingRules, nadRef NetworkRef) *NetworkMappingBlock {
	for i := range rules.Blocks {
		b := &rules.Blocks[i]
		if b.NetworkRef.NADNamespace == nadRef.NADNamespace &&
			b.NetworkRef.NADName == nadRef.NADName {
			return b
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// translateIP — ordered dispatch: explicit → CIDR → regex
// ---------------------------------------------------------------------------

// translateIP translates srcIP for the NAD identified by nadRef.
// Returns the translated IP and the RuleType that produced the result.
func translateIP(
	srcIP string,
	rules *NetworkMappingRules,
	dir TranslationDirection,
	nadRef NetworkRef,
) (string, RuleType, error) {
	block := findBlock(rules, nadRef)
	if block == nil {
		return "", "", fmt.Errorf("no networkMapping block found for NAD %s/%s",
			nadRef.NADNamespace, nadRef.NADName)
	}

	// Step 1: explicit mappings — most specific.
	if block.ExplicitMappings != nil {
		if result, ok := lookupExplicit(srcIP, block.ExplicitMappings, dir); ok {
			return result, RuleTypeExplicit, nil
		}
	}

	// Step 2: CIDR subnet-offset.
	if block.CIDRMapping != nil {
		if result, ok := applyCIDR(srcIP, block.CIDRMapping, dir); ok {
			return result, RuleTypeCIDR, nil
		}
	}

	// Step 3: regex fallback.
	if block.RegexRules != nil {
		result, err := applyRegexRules(srcIP, block.RegexRules, dir, rules.DR1, rules.DR2)
		if err != nil {
			return "", "", err
		}

		return result, RuleTypeRegex, nil
	}

	// No rule matched.
	dirStr := rules.DR1 + "-to-" + rules.DR2
	if dir == TranslationDirectionReverse {
		dirStr = rules.DR2 + "-to-" + rules.DR1
	}

	return "", "", fmt.Errorf(
		"no translation found for IP %q (NAD %s/%s, direction %s): "+
			"no explicit match, not in CIDR range, no regex match",
		srcIP, nadRef.NADNamespace, nadRef.NADName, dirStr)
}

// ---------------------------------------------------------------------------
// Explicit lookup
// ---------------------------------------------------------------------------

// lookupExplicit performs an O(1) map lookup.
// Forward: dr1IP → dr2IP.  Reverse: dr2IP → dr1IP.
// Returns ("", false) on miss.
func lookupExplicit(srcIP string, em *ExplicitMappings, dir TranslationDirection) (string, bool) {
	norm := normalizeIP(srcIP)

	if dir == TranslationDirectionForward {
		result, ok := em.Forward[norm]

		return result, ok
	}

	result, ok := em.Reverse[norm]

	return result, ok
}

// ---------------------------------------------------------------------------
// CIDR translation
// ---------------------------------------------------------------------------

// applyCIDR applies subnet-offset translation.
// Forward: translates srcIP from dr1Net to dr2Net.
// Reverse: translates srcIP from dr2Net to dr1Net.
// Returns ("", false) when the IP is not in the relevant subnet.
func applyCIDR(srcIP string, cm *CIDRMapping, dir TranslationDirection) (string, bool) {
	ip := net.ParseIP(srcIP)
	if ip == nil {
		return "", false
	}

	ip4 := ip.To4()
	if ip4 == nil {
		return "", false
	}

	var srcNet, dstNet *net.IPNet

	if dir == TranslationDirectionForward {
		srcNet, dstNet = cm.dr1Net, cm.dr2Net
	} else {
		srcNet, dstNet = cm.dr2Net, cm.dr1Net
	}

	if !srcNet.Contains(ip4) {
		return "", false
	}

	srcNetAddr := ipToUint32(srcNet.IP.To4())
	dstNetAddr := ipToUint32(dstNet.IP.To4())
	hostOffset := ipToUint32(ip4) - srcNetAddr
	dstIPAddr := dstNetAddr + hostOffset

	result := uint32ToIP(dstIPAddr)

	if !dstNet.Contains(result) {
		// Host offset exceeds destination subnet — treat as miss.
		return "", false
	}

	return result.String(), true
}

// ---------------------------------------------------------------------------
// Regex translation
// ---------------------------------------------------------------------------

// applyRegexRules applies the ordered list of rules for the given direction.
// The first matching rule wins.  Returns an error if no rule matches.
func applyRegexRules(srcIP string, rr *RegexRules, dir TranslationDirection, dr1, dr2 string) (string, error) {
	rules := rr.Forward
	dirStr := dr1 + "-to-" + dr2

	if dir == TranslationDirectionReverse {
		rules = rr.Reverse
		dirStr = dr2 + "-to-" + dr1
	}

	for _, rule := range rules {
		if !rule.compiled.MatchString(srcIP) {
			continue
		}

		result := rule.compiled.ReplaceAllString(srcIP, rule.Replacement)

		if net.ParseIP(result) == nil {
			return "", fmt.Errorf("regex pattern %q applied to IP %q produced invalid IP %q",
				rule.Pattern, srcIP, result)
		}

		return result, nil
	}

	return "", fmt.Errorf("no %s regex rule matched IP %q", dirStr, srcIP)
}

// ---------------------------------------------------------------------------
// IP normalization
// ---------------------------------------------------------------------------

// normalizeIP returns the canonical dotted-decimal form of an IPv4 address.
// Returns the original string unchanged if parsing or To4 conversion fails.
func normalizeIP(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			return v4.String()
		}
	}

	return ip
}

// directionFromCluster converts a source cluster name to a TranslationDirection.
// Forward = sourceCluster is dr1 (failover: dr1→dr2).
// Reverse = sourceCluster is dr2 (failback: dr2→dr1).
// Returns an error if sourceCluster matches neither.
func directionFromCluster(sourceCluster, dr1, dr2 string) (TranslationDirection, error) {
	switch sourceCluster {
	case dr1:
		return TranslationDirectionForward, nil
	case dr2:
		return TranslationDirectionReverse, nil
	default:
		return TranslationDirectionForward, fmt.Errorf(
			"sourceCluster %q is not a member of this DRPolicy (dr1=%q, dr2=%q)",
			sourceCluster, dr1, dr2)
	}
}

// ---------------------------------------------------------------------------
// DRPCInstance methods
// ---------------------------------------------------------------------------

// translateSourceIP translates srcIP using the source cluster name to
// determine direction (Forward = sourceCluster is dr1; Reverse = dr2).
//
// nadRef identifies which NetworkMapping block to use.
// On any failure the error is logged and srcIP is returned unchanged so
// that DR orchestration is never blocked by mapping errors.
func (d *DRPCInstance) translateSourceIP(srcIP, sourceCluster string, nadRef NetworkRef) string {
	if d.networkMappingRules == nil {
		return srcIP
	}

	dir, err := directionFromCluster(sourceCluster, d.networkMappingRules.DR1, d.networkMappingRules.DR2)
	if err != nil {
		d.log.Error(err, "IP translation failed; using source IP unchanged",
			"sourceIP", srcIP,
			"nad", nadRef.NADNamespace+"/"+nadRef.NADName)

		return srcIP
	}

	targetIP, ruleType, err := translateIP(srcIP, d.networkMappingRules, dir, nadRef)
	if err != nil {
		d.log.Error(err, "IP translation failed; using source IP unchanged",
			"sourceIP", srcIP,
			"nad", nadRef.NADNamespace+"/"+nadRef.NADName)

		return srcIP
	}

	d.log.V(1).Info("Translated IP",
		"network", nadRef.NADNamespace+"/"+nadRef.NADName,
		"sourceCluster", sourceCluster,
		"inputIP", srcIP,
		"ruleType", ruleType,
		"outputIP", targetIP)

	return targetIP
}

// ---------------------------------------------------------------------------
// translateSubnet — arithmetic helper (used by tests)
// ---------------------------------------------------------------------------

// translateSubnet translates srcIP from srcCIDR into the equivalent address in
// dstCIDR by preserving the host offset within the source subnet.
//
// Both CIDRs must have equal prefix lengths.
// When preserveHostPart is false the first usable host in dstCIDR is returned.
func translateSubnet(srcIP, srcCIDR, dstCIDR string, preserveHostPart bool) (string, error) {
	ip := net.ParseIP(srcIP)
	if ip == nil {
		return "", fmt.Errorf("invalid source IP %q", srcIP)
	}

	ip = ip.To4()
	if ip == nil {
		return "", fmt.Errorf("only IPv4 is supported (got %q)", srcIP)
	}

	_, srcNet, err := net.ParseCIDR(srcCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid source CIDR %q: %w", srcCIDR, err)
	}

	_, dstNet, err := net.ParseCIDR(dstCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid destination CIDR %q: %w", dstCIDR, err)
	}

	if !srcNet.Contains(ip) {
		return "", fmt.Errorf("source IP %q is not within source CIDR %q", srcIP, srcCIDR)
	}

	srcOnes, srcBits := srcNet.Mask.Size()
	dstOnes, dstBits := dstNet.Mask.Size()

	if srcBits != dstBits {
		return "", fmt.Errorf("source and destination CIDRs have different address families")
	}

	if srcOnes != dstOnes {
		return "", fmt.Errorf(
			"source CIDR /%d and destination CIDR /%d have different prefix lengths; "+
				"equal prefix lengths are required for subnet-based host-offset translation",
			srcOnes, dstOnes)
	}

	if !preserveHostPart {
		dstAddr := ipToUint32(dstNet.IP.To4())

		return uint32ToIP(dstAddr + 1).String(), nil
	}

	srcNetAddr := ipToUint32(srcNet.IP.To4())
	srcIPAddr := ipToUint32(ip)
	hostOffset := srcIPAddr - srcNetAddr

	dstNetAddr := ipToUint32(dstNet.IP.To4())
	dstIPAddr := dstNetAddr + hostOffset

	result := uint32ToIP(dstIPAddr)
	if !dstNet.Contains(result) {
		return "", fmt.Errorf(
			"translated IP %s (offset %d) is outside destination CIDR %q",
			result, hostOffset, dstCIDR)
	}

	return result.String(), nil
}

// ---------------------------------------------------------------------------
// Internal arithmetic helpers
// ---------------------------------------------------------------------------

func ipToUint32(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip)
}

const ipv4Len = 4

func uint32ToIP(n uint32) net.IP {
	b := make([]byte, ipv4Len)
	binary.BigEndian.PutUint32(b, n)

	return net.IP(b)
}

func extractClusterIPs(
	blockIdx, rowIdx int,
	pair rawClusterIPPair,
	dr1, dr2 string,
) (string, string, error) {
	dr1IP, hasDR1 := pair[dr1]
	if !hasDR1 {
		return "", "", fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: missing cluster key %q",
			blockIdx, rowIdx, dr1,
		)
	}

	dr2IP, hasDR2 := pair[dr2]
	if !hasDR2 {
		return "", "", fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: missing cluster key %q",
			blockIdx, rowIdx, dr2,
		)
	}

	for k := range pair {
		if k != dr1 && k != dr2 {
			return "", "", fmt.Errorf(
				"networkMappings[%d]: explicitMappings[%d]: unrecognized key %q (expected %q or %q)",
				blockIdx, rowIdx, k, dr1, dr2,
			)
		}
	}

	return dr1IP, dr2IP, nil
}

func normalizeExplicitIPs(
	blockIdx, rowIdx int,
	dr1, dr2 string,
	dr1IP, dr2IP string,
) (string, string, error) {
	dr1IP4 := net.ParseIP(dr1IP).To4()
	if dr1IP4 == nil {
		return "", "", fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: invalid or non-IPv4 %s address %q",
			blockIdx, rowIdx, dr1, dr1IP,
		)
	}

	dr2IP4 := net.ParseIP(dr2IP).To4()
	if dr2IP4 == nil {
		return "", "", fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: invalid or non-IPv4 %s address %q",
			blockIdx, rowIdx, dr2, dr2IP,
		)
	}

	dr1Norm := dr1IP4.String()
	dr2Norm := dr2IP4.String()

	if dr1Norm == dr2Norm {
		return "", "", fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: %s and %s IP are the same (%q); direction ambiguous",
			blockIdx, rowIdx,
			dr1, dr2,
			dr1Norm,
		)
	}

	return dr1Norm, dr2Norm, nil
}

func validateExplicitMappingCollision(
	blockIdx, rowIdx int,
	dr1Norm, dr2Norm string,
	dr1, dr2 string,
	fwd, rev map[string]string,
) error {
	if prev, dup := fwd[dr1Norm]; dup {
		return fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: duplicate %s IP %q (already maps to %q)",
			blockIdx, rowIdx,
			dr1,
			dr1Norm,
			prev,
		)
	}

	if prev, dup := rev[dr2Norm]; dup {
		return fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: duplicate %s IP %q (already maps to %q)",
			blockIdx, rowIdx,
			dr2,
			dr2Norm,
			prev,
		)
	}

	if _, collision := rev[dr1Norm]; collision {
		return fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: IP %q appears as both a %s IP and a %s IP; direction would be ambiguous",
			blockIdx,
			rowIdx,
			dr1Norm,
			dr1,
			dr2,
		)
	}

	if _, collision := fwd[dr2Norm]; collision {
		return fmt.Errorf(
			"networkMappings[%d]: explicitMappings[%d]: IP %q appears as both a %s IP and a %s IP; direction would be ambiguous",
			blockIdx,
			rowIdx,
			dr2Norm,
			dr2,
			dr1,
		)
	}

	return nil
}
