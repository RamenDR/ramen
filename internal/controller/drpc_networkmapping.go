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
// The ConfigMap's "mappings.yaml" data key contains a YAML document:
//
//	patternTranslation:          # optional; provide for regex-based subnet mapping
//	  forwardPattern:     "^192\\.168\\.100\\.(\\d+)$"
//	  forwardReplacement: "192.168.200.$1"
//	  reversePattern:     "^192\\.168\\.200\\.(\\d+)$"
//	  reverseReplacement: "192.168.100.$1"
//
//	explicitMappings:            # optional; per-VM overrides checked before pattern
//	  - sourceIP: "192.168.100.51"
//	    destIP:   "192.168.200.55"
//	    vmName:   "vm-override"  # optional, informational only
//
// At least one of patternTranslation or explicitMappings must be present.
// When both are present, explicitMappings is checked first; unmatched IPs
// fall through to patternTranslation.

import (
	"context"
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

// TranslationDirection controls which side (forward = failover, reverse = failback)
// of the pattern/explicit table is used.
type TranslationDirection int

const (
	TranslationDirectionForward TranslationDirection = iota
	TranslationDirectionReverse
)

// ---------------------------------------------------------------------------
// Public types — ConfigMap data model
// ---------------------------------------------------------------------------

// NetworkMappingRules is the top-level structure parsed from the ConfigMap's
// "mappings.yaml" data key.  No translationMethod field is required;
// the algorithm is driven by which data fields are populated.
type NetworkMappingRules struct {
	// PatternTranslation holds the regex patterns for subnet-to-subnet mapping.
	// +optional
	PatternTranslation *PatternTranslation `yaml:"patternTranslation,omitempty"`

	// ExplicitMappings is the per-VM static IP mapping table.
	// Entries here take priority over patternTranslation.
	// +optional
	ExplicitMappings []ExplicitMapping `yaml:"explicitMappings,omitempty"`
}

// PatternTranslation carries the forward and reverse regex patterns.
type PatternTranslation struct {
	// ForwardPattern is a Go regexp applied to the source IP during failover.
	// Capture groups can be referenced as $1, $2, … in ForwardReplacement.
	ForwardPattern string `yaml:"forwardPattern"`

	// ForwardReplacement is the replacement template for ForwardPattern.
	ForwardReplacement string `yaml:"forwardReplacement"`

	// ReversePattern is a Go regexp applied to the translated IP during failback.
	ReversePattern string `yaml:"reversePattern"`

	// ReverseReplacement is the replacement template for ReversePattern.
	ReverseReplacement string `yaml:"reverseReplacement"`
}

// ExplicitMapping maps one source IP to one destination IP.
type ExplicitMapping struct {
	// SourceIP is the VM IP on the source cluster.
	SourceIP string `yaml:"sourceIP"`

	// DestIP is the VM IP on the destination cluster.
	DestIP string `yaml:"destIP"`
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
// An error is returned only when the ConfigMap exists but cannot be fetched or
// parsed.
func (m *DRPCNetworkMappingManager) LoadNetworkMapping(
	ctx context.Context,
	drpc *rmn.DRPlacementControl,
) (*NetworkMappingRules, error) {
	cmName, ok := drpc.GetAnnotations()[DRPCNetworkMappingAnnotation]
	if !ok || cmName == "" {
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

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		return nil, fmt.Errorf("failed to parse network-mapping ConfigMap %q: %w", cmName, err)
	}

	m.log.Info("Loaded network-mapping ConfigMap",
		"configmap", cmName,
		"hasPattern", rules.PatternTranslation != nil,
		"explicitEntries", len(rules.ExplicitMappings))

	return rules, nil
}

// TranslateIP translates srcIP using the rules and direction.
//
//   - Forward (failover): applies ForwardPattern / forward explicit lookup.
//   - Reverse (failback): applies ReversePattern / reverse explicit lookup
//     (the dest→source direction of the explicit table).
//
// The universal algorithm (regardless of translationMethod):
//  1. If explicitMappings are present, look up srcIP in the table first.
//     A hit returns immediately; a miss falls through to pattern.
//  2. If patternTranslation is present, match and replace via regex.
//  3. Both absent → error (caught at validation time, unreachable at runtime).
func (m *DRPCNetworkMappingManager) TranslateIP(
	srcIP string,
	rules *NetworkMappingRules,
	dir TranslationDirection,
) (string, error) {
	result, err := translateIP(srcIP, rules, dir)
	if err != nil {
		return "", err
	}

	m.log.V(1).Info("Translated IP",
		"source", srcIP,
		"target", result,
		"direction", dir)

	return result, nil
}

// ---------------------------------------------------------------------------
// ParseNetworkMappingConfigMap
// ---------------------------------------------------------------------------

// ParseNetworkMappingConfigMap parses a ConfigMap that follows the
// network-mapping format described at the top of this file.
//
// The ConfigMap must have a "mappings.yaml" data key.
func ParseNetworkMappingConfigMap(cm *corev1.ConfigMap) (*NetworkMappingRules, error) {
	const dataKey = "mappings.yaml"

	raw, ok := cm.Data[dataKey]
	if !ok {
		return nil, fmt.Errorf("ConfigMap %q/%q has no %q data key",
			cm.Namespace, cm.Name, dataKey)
	}

	rules := &NetworkMappingRules{}
	if err := sigsyaml.Unmarshal([]byte(raw), rules); err != nil {
		return nil, fmt.Errorf("failed to parse %q in ConfigMap %q: %w",
			dataKey, cm.Name, err)
	}

	if err := validateNetworkMappingRules(rules); err != nil {
		return nil, fmt.Errorf("invalid network mapping in ConfigMap %q: %w",
			cm.Name, err)
	}

	return rules, nil
}

// ---------------------------------------------------------------------------
// translateIP — universal probe-driven algorithm
// ---------------------------------------------------------------------------

// translateIP translates srcIP using the data present in rules, regardless of
// the translationMethod field. The method field is used only for validation and
// documentation; at runtime the presence of data drives the algorithm:
//
//  1. Explicit table (if present): look up srcIP. Hit → return. Miss → fall through.
//  2. Pattern (if present): match srcIP against forward/reverse regex. Hit → return.
//  3. Neither data present → error (prevented by validateNetworkMappingRules).
//
// This means "explicit", "pattern", and "pattern-with-overrides" all run the
// same code — the label is redundant at translation time.
func translateIP(srcIP string, rules *NetworkMappingRules, dir TranslationDirection) (string, error) {
	// Step 1: explicit table — always checked first.
	if len(rules.ExplicitMappings) > 0 {
		if translated, ok := lookupExplicit(srcIP, rules.ExplicitMappings, dir); ok {
			return translated, nil
		}
		// Miss: fall through to pattern if available.
	}

	// Step 2: pattern — used when explicit table is absent or had no match.
	if rules.PatternTranslation != nil {
		return applyPatternTranslation(srcIP, rules.PatternTranslation, dir)
	}

	// Step 3: no data — should never reach here after validation.
	return "", fmt.Errorf("no translation found for IP %q (no explicit entry and no pattern)", srcIP)
}

// ---------------------------------------------------------------------------
// Pattern translation
// ---------------------------------------------------------------------------

// applyPatternTranslation applies the forward or reverse regex pattern to srcIP
// and returns the replacement string.
func applyPatternTranslation(
	srcIP string,
	pt *PatternTranslation,
	dir TranslationDirection,
) (string, error) {
	if pt == nil {
		return "", fmt.Errorf("patternTranslation is required for pattern-based methods")
	}

	var pattern, replacement string
	if dir == TranslationDirectionForward {
		pattern = pt.ForwardPattern
		replacement = pt.ForwardReplacement
	} else {
		pattern = pt.ReversePattern
		replacement = pt.ReverseReplacement
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	if !re.MatchString(srcIP) {
		dirStr := "forward"
		if dir == TranslationDirectionReverse {
			dirStr = "reverse"
		}

		return "", fmt.Errorf("IP %q does not match %s pattern %q",
			srcIP, dirStr, pattern)
	}

	// regexp.Regexp.ReplaceAllString handles $N capture-group references.
	result := re.ReplaceAllString(srcIP, replacement)

	if net.ParseIP(result) == nil {
		return "", fmt.Errorf("translation of IP %q with pattern %q produced invalid IP %q",
			srcIP, pattern, result)
	}

	return result, nil
}

// normalizeIP returns the canonical string form of an IP address via net.ParseIP.
// Returns the original string unchanged if parsing fails (callers already
// validate IPs at parse time so this path is unreachable in practice).
func normalizeIP(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil {
		return parsed.String()
	}

	return ip
}

// lookupExplicit performs the table lookup for one IP in the given direction.
// Returns (translatedIP, true) on hit, ("", false) on miss.
// Both the search key and the stored values are normalized so that different
// textual representations of the same IP (e.g. IPv6 variants) match correctly.
func lookupExplicit(
	srcIP string,
	mappings []ExplicitMapping,
	dir TranslationDirection,
) (string, bool) {
	norm := normalizeIP(srcIP)

	for _, m := range mappings {
		if dir == TranslationDirectionForward && normalizeIP(m.SourceIP) == norm {
			return m.DestIP, true
		}

		if dir == TranslationDirectionReverse && normalizeIP(m.DestIP) == norm {
			return m.SourceIP, true
		}
	}

	return "", false
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// validateNetworkMappingRules checks that at least one data source is present
// and that all provided data is structurally valid.
// No translationMethod field is consulted — the data presence drives everything.
func validateNetworkMappingRules(rules *NetworkMappingRules) error {
	if rules.PatternTranslation == nil && len(rules.ExplicitMappings) == 0 {
		return fmt.Errorf("mappings.yaml must contain patternTranslation, explicitMappings, or both")
	}

	if rules.PatternTranslation != nil {
		if err := validatePatternTranslation(rules.PatternTranslation); err != nil {
			return err
		}
	}

	if len(rules.ExplicitMappings) > 0 {
		return validateExplicitMappings(rules.ExplicitMappings)
	}

	return nil
}

func validatePatternTranslation(pt *PatternTranslation) error {
	if pt.ForwardPattern == "" {
		return fmt.Errorf("patternTranslation.forwardPattern is required")
	}

	if pt.ForwardReplacement == "" {
		return fmt.Errorf("patternTranslation.forwardReplacement is required")
	}

	if pt.ReversePattern == "" {
		return fmt.Errorf("patternTranslation.reversePattern is required")
	}

	if pt.ReverseReplacement == "" {
		return fmt.Errorf("patternTranslation.reverseReplacement is required")
	}

	if _, err := regexp.Compile(pt.ForwardPattern); err != nil {
		return fmt.Errorf("invalid forwardPattern %q: %w", pt.ForwardPattern, err)
	}

	if _, err := regexp.Compile(pt.ReversePattern); err != nil {
		return fmt.Errorf("invalid reversePattern %q: %w", pt.ReversePattern, err)
	}

	return nil
}

// parseExplicitMappingIPs validates a single ExplicitMapping entry and returns
// the normalized string forms of its sourceIP and destIP.
func parseExplicitMappingIPs(i int, m ExplicitMapping) (srcNorm, dstNorm string, err error) {
	if m.SourceIP == "" {
		return "", "", fmt.Errorf("explicitMappings[%d]: sourceIP is required", i)
	}

	if m.DestIP == "" {
		return "", "", fmt.Errorf("explicitMappings[%d]: destIP is required", i)
	}

	srcParsed := net.ParseIP(m.SourceIP)
	if srcParsed == nil {
		return "", "", fmt.Errorf("explicitMappings[%d]: invalid sourceIP %q", i, m.SourceIP)
	}

	dstParsed := net.ParseIP(m.DestIP)
	if dstParsed == nil {
		return "", "", fmt.Errorf("explicitMappings[%d]: invalid destIP %q", i, m.DestIP)
	}

	return srcParsed.String(), dstParsed.String(), nil
}

func validateExplicitMappings(mappings []ExplicitMapping) error {
	// Pass 1: per-entry field/format checks; build normalized IP sets for pass 2.
	srcIPs := make(map[string]int, len(mappings)) // normalized IP → first-seen index
	dstIPs := make(map[string]int, len(mappings))

	for i, m := range mappings {
		srcNorm, dstNorm, err := parseExplicitMappingIPs(i, m)
		if err != nil {
			return err
		}

		if prev, dup := srcIPs[srcNorm]; dup {
			return fmt.Errorf("explicitMappings[%d]: duplicate sourceIP %q (already at index %d)",
				i, srcNorm, prev)
		}

		srcIPs[srcNorm] = i

		if prev, dup := dstIPs[dstNorm]; dup {
			return fmt.Errorf("explicitMappings[%d]: duplicate destIP %q (already at index %d)",
				i, dstNorm, prev)
		}

		dstIPs[dstNorm] = i
	}

	// Pass 2: reject any IP that appears in both columns — direction would be
	// ambiguous because directionFromExplicitOk checks sourceIP first and would
	// always select Forward, making the reverse path unreachable.
	for ip, srcIdx := range srcIPs {
		if dstIdx, collision := dstIPs[ip]; collision {
			return fmt.Errorf(
				"explicitMappings: IP %q appears as sourceIP (index %d) and destIP (index %d); "+
					"direction would be ambiguous",
				ip, srcIdx, dstIdx)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// DRPCInstance.translateSourceIP — replaces the stub in buildStaticIPTranslationSpec
// ---------------------------------------------------------------------------

// directionForIP determines whether to apply the Forward or Reverse translation
// for a given IP address by probing the configured data directly.
//
// # Why cluster-name comparison is unreliable
//
// Spec.PreferredCluster is user-mutable and changes after every Relocate.
// LastAppDeploymentCluster is stamped by updateUserPlacementRule only on
// non-dry-run transitions, so it may lag or be absent on hub recovery.
// Neither field reliably anchors "which subnet is forward".
//
// # The IP-driven approach
//
// The function mirrors the universal lookup order used by translateIP so that
// direction detection and translation are always in lockstep:
//
//  1. If explicitMappings are present, check which column the IP appears in.
//     sourceIP → Forward; destIP → Reverse. Hit returns immediately.
//  2. If patternTranslation is present, match the IP against forwardPattern
//     then reversePattern. First match wins.
//  3. Neither matches → Forward is returned; the caller will receive a
//     descriptive error from translateIP.
//
// This ordering means an override address whose IPs lie outside the pattern
// subnets is still resolved correctly by the explicit table (step 1) before
// the pattern probe (step 2) is attempted.
func (d *DRPCInstance) directionForIP(srcIP string) TranslationDirection {
	rules := d.networkMappingRules
	if rules == nil {
		return TranslationDirectionForward
	}

	if len(rules.ExplicitMappings) > 0 {
		if dir, ok := directionFromExplicitOk(srcIP, rules.ExplicitMappings); ok {
			return dir
		}
		// Miss in explicit table — fall through to pattern probe.
	}

	if rules.PatternTranslation != nil {
		return directionFromPattern(srcIP, rules.PatternTranslation)
	}

	return TranslationDirectionForward
}

// directionFromPattern probes forwardPattern then reversePattern to decide
// translation direction. Returns Forward if neither pattern matches.
func directionFromPattern(srcIP string, pt *PatternTranslation) TranslationDirection {
	if pt == nil {
		return TranslationDirectionForward
	}

	if pt.ForwardPattern != "" {
		if re, err := regexp.Compile(pt.ForwardPattern); err == nil && re.MatchString(srcIP) {
			return TranslationDirectionForward
		}
	}

	if pt.ReversePattern != "" {
		if re, err := regexp.Compile(pt.ReversePattern); err == nil && re.MatchString(srcIP) {
			return TranslationDirectionReverse
		}
	}

	return TranslationDirectionForward
}

// directionFromExplicitOk checks which column (sourceIP vs destIP) the IP
// appears in. Returns (direction, true) on hit, (Forward, false) on miss.
// IPs are normalized before comparison so variant spellings of the same
// address (e.g. IPv6 shorthand forms) match correctly.
func directionFromExplicitOk(srcIP string, mappings []ExplicitMapping) (TranslationDirection, bool) {
	norm := normalizeIP(srcIP)

	for _, m := range mappings {
		if normalizeIP(m.SourceIP) == norm {
			return TranslationDirectionForward, true
		}
	}

	for _, m := range mappings {
		if normalizeIP(m.DestIP) == norm {
			return TranslationDirectionReverse, true
		}
	}

	return TranslationDirectionForward, false
}

// translateSourceIP translates a single IP address discovered on the primary
// VRG into the address it should have on the secondary (homeCluster).
//
// The call site is buildStaticIPTranslationSpec, which passes every
// address from primaryVRG.Status.StaticIPDiscoveryStatus through this function.
//
// Direction is determined by probing the IP against the configured patterns
// (see directionForIP), so this function works correctly regardless of which
// cluster is currently primary and regardless of any user edits to
// Spec.PreferredCluster after initial deployment.
//
// On translation failure the error is logged and srcIP is returned unchanged
// so a single bad address does not abort the entire VRG spec update.
func (d *DRPCInstance) translateSourceIP(srcIP string) string {
	if d.networkMappingRules == nil {
		return srcIP
	}

	dir := d.directionForIP(srcIP)

	mgr := NewDRPCNetworkMappingManager(d.reconciler.Client, d.log)

	targetIP, err := mgr.TranslateIP(srcIP, d.networkMappingRules, dir)
	if err != nil {
		d.log.Error(err, "IP translation failed; using source IP unchanged",
			"sourceIP", srcIP,
			"direction", dir)

		return srcIP
	}

	d.log.V(1).Info("Translated IP",
		"sourceIP", srcIP,
		"targetIP", targetIP,
		"direction", dir)

	return targetIP
}
