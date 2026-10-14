// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers //nolint:testpackage

// Unit tests for drpc_networkmapping.go (schema v1).
//
// Rule types tested:
//   explicitMappings — per-VM exact IP overrides, O(1) map lookup
//   cidr             — subnet-offset translation (bidirectional)
//   regexMappings    — directional regex fallback (lowest priority)
//
// Direction is always derived from the source cluster name, never inferred
// from the IP value.
//
// Run with: go test ./internal/controller/ -run 'TestNM|TestTranslate'

import (
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// Shared constants
// ---------------------------------------------------------------------------

const (
	testDR1 = "east-cluster"
	testDR2 = "west-cluster"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeCM(yaml string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "nm", Namespace: "ns"},
		Data:       map[string]string{"mappings.yaml": yaml},
	}
}

// mustParse parses yaml with testDR1/testDR2 and fails the test on error.
func mustParse(t *testing.T, yaml string) *NetworkMappingRules {
	t.Helper()

	rules, err := ParseNetworkMappingConfigMap(makeCM(yaml), testDR1, testDR2)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	return rules
}

// mustParseErr parses yaml and returns the error (nil means parse succeeded).
func mustParseErr(t *testing.T, yaml string) error {
	t.Helper()

	_, err := ParseNetworkMappingConfigMap(makeCM(yaml), testDR1, testDR2)

	return err
}

func nadRef(name string) NetworkRef {
	return NetworkRef{NADNamespace: "default", NADName: name}
}

// newDRPCInstanceForTest builds the minimal DRPCInstance needed for
// translateSourceIP tests.
func newDRPCInstanceForTest(rules *NetworkMappingRules) *DRPCInstance {
	return &DRPCInstance{
		reconciler:          &DRPlacementControlReconciler{},
		log:                 logr.Discard(),
		instance:            &rmn.DRPlacementControl{},
		networkMappingRules: rules,
	}
}

// ---------------------------------------------------------------------------
// translateSubnet helper — unchanged across schema versions
// ---------------------------------------------------------------------------

func TestTranslateSubnet_PreserveHostPart(t *testing.T) {
	tests := []struct {
		name     string
		srcIP    string
		srcCIDR  string
		dstCIDR  string
		preserve bool
		want     string
		wantErr  bool
	}{
		{"/24 offset", "192.168.10.25", "192.168.10.0/24", "10.40.0.0/24", true, "10.40.0.25", false},
		{"/16 offset", "10.100.1.200", "10.100.0.0/16", "10.200.0.0/16", true, "10.200.1.200", false},
		{"gateway", "192.168.10.1", "192.168.10.0/24", "10.40.0.0/24", true, "10.40.0.1", false},
		{"no preserve", "192.168.10.25", "192.168.10.0/24", "10.40.0.0/24", false, "10.40.0.1", false},
		{"bad ip", "not-an-ip", "192.168.10.0/24", "10.40.0.0/24", true, "", true},
		{"out of cidr", "192.168.20.5", "192.168.10.0/24", "10.40.0.0/24", true, "", true},
		{"prefix mismatch", "192.168.10.1", "192.168.10.0/24", "10.0.0.0/16", true, "", true},
		{"network addr", "192.168.10.0", "192.168.10.0/24", "10.40.0.0/24", true, "10.40.0.0", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := translateSubnet(tc.srcIP, tc.srcCIDR, tc.dstCIDR, tc.preserve)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (result=%q)", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// directionFromCluster
// ---------------------------------------------------------------------------

func TestDirectionFromCluster_DR1(t *testing.T) {
	dir, err := directionFromCluster(testDR1, testDR1, testDR2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dir != TranslationDirectionForward {
		t.Errorf("dr1 source → Forward, got %v", dir)
	}
}

func TestDirectionFromCluster_DR2(t *testing.T) {
	dir, err := directionFromCluster(testDR2, testDR1, testDR2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dir != TranslationDirectionReverse {
		t.Errorf("dr2 source → Reverse, got %v", dir)
	}
}

func TestDirectionFromCluster_Unknown(t *testing.T) {
	_, err := directionFromCluster("other-cluster", testDR1, testDR2)
	if err == nil {
		t.Error("expected error for unknown source cluster")
	}
}

// ---------------------------------------------------------------------------
// YAML fixtures — schema v1
// ---------------------------------------------------------------------------

// cidrOnlyYAML — single NAD with only a CIDR block.
const cidrOnlyYAML = `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName:      storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.120.0/24
`

// explicitOnlyYAML — single NAD with only explicit mappings.
const explicitOnlyYAML = `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName:      backup
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.120.10"
      - east-cluster: "192.168.100.20"
        west-cluster: "192.168.120.20"
`

// regexOnlyYAML — single NAD with only regex mappings.
const regexOnlyYAML = `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName:      mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern:     "^172\\.16\\.100\\.(\\d+)$"
          replacement: "10.20.30.$1"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

// allThreeYAML — single NAD with all three rule types: explicit + cidr + regex.
// 192.168.100.51 (explicit) takes priority over the CIDR range it sits in.
const allThreeYAML = `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName:      backup
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.110.10"
      - east-cluster: "192.168.100.51"
        west-cluster: "192.168.110.58"
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern:     "^172\\.16\\.100\\.(\\d+)$"
          replacement: "10.20.30.$1"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

// twoBlockYAML — two NADs with different rule types.
const twoBlockYAML = `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName:      storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.120.0/24

  - networkRef:
      nadNamespace: default
      nadName:      backup
    explicitMappings:
      - east-cluster: "10.0.0.1"
        west-cluster: "10.0.1.1"
`

// ---------------------------------------------------------------------------
// Parse tests — structure validation
// ---------------------------------------------------------------------------

func TestNMParse_CIDRBlock(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)

	if len(rules.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(rules.Blocks))
	}

	b := rules.Blocks[0]

	if b.NetworkRef.NADNamespace != "default" || b.NetworkRef.NADName != "storage" {
		t.Errorf("unexpected networkRef: %+v", b.NetworkRef)
	}

	if b.CIDRMapping == nil {
		t.Fatal("CIDRMapping is nil")
	}

	if b.CIDRMapping.DR1CIDR != "192.168.100.0/24" {
		t.Errorf("DR1CIDR = %q", b.CIDRMapping.DR1CIDR)
	}

	if b.CIDRMapping.DR2CIDR != "192.168.120.0/24" {
		t.Errorf("DR2CIDR = %q", b.CIDRMapping.DR2CIDR)
	}

	if b.ExplicitMappings != nil {
		t.Error("ExplicitMappings should be nil for a cidr-only block")
	}

	if b.RegexRules != nil {
		t.Error("RegexRules should be nil for a cidr-only block")
	}
}

func TestNMParse_ExplicitBlock(t *testing.T) {
	rules := mustParse(t, explicitOnlyYAML)

	if len(rules.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(rules.Blocks))
	}

	b := rules.Blocks[0]

	if b.NetworkRef.NADName != "backup" {
		t.Errorf("unexpected NAD name: %q", b.NetworkRef.NADName)
	}

	if b.ExplicitMappings == nil {
		t.Fatal("ExplicitMappings is nil")
	}

	if len(b.ExplicitMappings.Forward) != 2 {
		t.Errorf("expected 2 forward entries, got %d", len(b.ExplicitMappings.Forward))
	}

	if b.ExplicitMappings.Forward["192.168.100.10"] != "192.168.120.10" {
		t.Errorf("forward[192.168.100.10] = %q", b.ExplicitMappings.Forward["192.168.100.10"])
	}

	if b.ExplicitMappings.Reverse["192.168.120.10"] != "192.168.100.10" {
		t.Errorf("reverse[192.168.120.10] = %q", b.ExplicitMappings.Reverse["192.168.120.10"])
	}

	if b.CIDRMapping != nil {
		t.Error("CIDRMapping should be nil for an explicit-only block")
	}

	if b.RegexRules != nil {
		t.Error("RegexRules should be nil for an explicit-only block")
	}
}

func TestNMParse_RegexBlock(t *testing.T) {
	rules := mustParse(t, regexOnlyYAML)

	if len(rules.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(rules.Blocks))
	}

	b := rules.Blocks[0]

	if b.NetworkRef.NADName != "mgmt" {
		t.Errorf("unexpected NAD name: %q", b.NetworkRef.NADName)
	}

	if b.RegexRules == nil {
		t.Fatal("RegexRules is nil")
	}

	if len(b.RegexRules.Forward) != 1 {
		t.Errorf("expected 1 forward rule, got %d", len(b.RegexRules.Forward))
	}

	if len(b.RegexRules.Reverse) != 1 {
		t.Errorf("expected 1 reverse rule, got %d", len(b.RegexRules.Reverse))
	}

	if b.RegexRules.Forward[0].Pattern != `^172\.16\.100\.(\d+)$` {
		t.Errorf("forward pattern = %q", b.RegexRules.Forward[0].Pattern)
	}

	if b.RegexRules.Forward[0].Replacement != "10.20.30.$1" {
		t.Errorf("forward replacement = %q", b.RegexRules.Forward[0].Replacement)
	}

	if b.CIDRMapping != nil {
		t.Error("CIDRMapping should be nil for a regex-only block")
	}

	if b.ExplicitMappings != nil {
		t.Error("ExplicitMappings should be nil for a regex-only block")
	}
}

func TestNMParse_AllThreeRuleTypes(t *testing.T) {
	rules := mustParse(t, allThreeYAML)

	if len(rules.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(rules.Blocks))
	}

	b := rules.Blocks[0]

	if b.ExplicitMappings == nil || len(b.ExplicitMappings.Forward) != 2 {
		t.Errorf("expected 2 explicit mappings")
	}

	if b.CIDRMapping == nil {
		t.Error("CIDRMapping should be non-nil")
	}

	if b.RegexRules == nil {
		t.Error("RegexRules should be non-nil")
	}
}

func TestNMParse_TwoBlocks(t *testing.T) {
	rules := mustParse(t, twoBlockYAML)

	if len(rules.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(rules.Blocks))
	}

	if rules.Blocks[0].NetworkRef.NADName != "storage" {
		t.Errorf("block[0] NAD = %q", rules.Blocks[0].NetworkRef.NADName)
	}

	if rules.Blocks[1].NetworkRef.NADName != "backup" {
		t.Errorf("block[1] NAD = %q", rules.Blocks[1].NetworkRef.NADName)
	}
}

func TestNMParse_ClusterNamesStoredInRules(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)

	if rules.DR1 != testDR1 {
		t.Errorf("DR1 = %q, want %q", rules.DR1, testDR1)
	}

	if rules.DR2 != testDR2 {
		t.Errorf("DR2 = %q, want %q", rules.DR2, testDR2)
	}
}

func TestNMParse_VersionStoredInRules(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)

	if rules.Version != "v1" {
		t.Errorf("Version = %q, want \"v1\"", rules.Version)
	}
}

// ---------------------------------------------------------------------------
// Version validation tests
// ---------------------------------------------------------------------------

func TestNMParse_Version_Missing(t *testing.T) {
	yaml := `
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
`

	if err := mustParseErr(t, yaml); err == nil {
		t.Error("expected error when version field is absent")
	}
}

func TestNMParse_Version_Empty(t *testing.T) {
	yaml := `
version: ""
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
`

	if err := mustParseErr(t, yaml); err == nil {
		t.Error("expected error when version field is empty string")
	}
}

func TestNMParse_Version_Unsupported(t *testing.T) {
	yaml := `
version: "v2"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
`

	if err := mustParseErr(t, yaml); err == nil {
		t.Error("expected error for unsupported schema version")
	}
}

func TestNMParse_Version_V1Accepted(t *testing.T) {
	// Explicit sanity check: v1 must parse without error and version must be
	// propagated into the returned rules.
	rules := mustParse(t, cidrOnlyYAML)

	if rules.Version != "v1" {
		t.Errorf("expected Version \"v1\", got %q", rules.Version)
	}
}

// ---------------------------------------------------------------------------
// Parse error tests — missing data key and cluster name validation
// ---------------------------------------------------------------------------

func TestNMParse_MissingDataKey(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "nm", Namespace: "ns"},
		Data:       map[string]string{"wrong-key": "x"},
	}

	_, err := ParseNetworkMappingConfigMap(cm, testDR1, testDR2)
	if err == nil {
		t.Error("expected error for missing mappings.yaml key")
	}
}

func TestNMParse_EmptyClusterName(t *testing.T) {
	_, err := ParseNetworkMappingConfigMap(makeCM(cidrOnlyYAML), "", testDR2)
	if err == nil {
		t.Error("expected error for empty dr1 cluster name")
	}
}

func TestNMParse_SameClusterName(t *testing.T) {
	_, err := ParseNetworkMappingConfigMap(makeCM(cidrOnlyYAML), "same", "same")
	if err == nil {
		t.Error("expected error when dr1 == dr2")
	}
}

// ---------------------------------------------------------------------------
// Validation error tests — networkRef
// ---------------------------------------------------------------------------

func TestNMValidation_EmptyList(t *testing.T) {
	yaml := `
version: "v1"
networkMappings: []`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty networkMappings list")
	}

	if !strings.Contains(err.Error(), "networkMappings list must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_MissingNADNamespace(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadName: backup
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing nadNamespace")
	}

	if !strings.Contains(err.Error(), "nadNamespace is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_MissingNADName(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing nadName")
	}

	if !strings.Contains(err.Error(), "nadName is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_DuplicateNetworkRef(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
  - networkRef:
      nadNamespace: default
      nadName: backup
    cidr:
      east-cluster: 10.0.0.0/24
      west-cluster: 10.0.1.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for duplicate networkRef")
	}

	if !strings.Contains(err.Error(), "duplicate networkRef") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_NoRuleType(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error when no cidr/explicitMappings/regexMappings present")
	}

	if !strings.Contains(err.Error(), "at least one of cidr, explicitMappings, or regexMappings is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Validation error tests — cidr section
// ---------------------------------------------------------------------------

func TestNMValidation_CIDR_MissingDR1Key(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      west-cluster: 192.168.110.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing dr1 key in cidr")
	}

	if !strings.Contains(err.Error(), "missing cluster key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_CIDR_MissingDR2Key(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing dr2 key in cidr")
	}

	if !strings.Contains(err.Error(), "missing cluster key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_CIDR_UnknownKey(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.110.0/24
      unknown-cluster: 10.0.0.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for unrecognized key in cidr")
	}

	if !strings.Contains(err.Error(), "unrecognized key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_CIDR_InvalidCIDR(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: not-a-cidr
      west-cluster: 192.168.110.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}

	if !strings.Contains(err.Error(), "invalid CIDR") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_CIDR_PrefixMismatch(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.0.0/16
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for mismatched prefix lengths")
	}

	if !strings.Contains(err.Error(), "different prefix lengths") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_CIDR_IdenticalNetworks(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: 192.168.100.0/24
      west-cluster: 192.168.100.0/24
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error when dr1 and dr2 CIDR are the same network")
	}

	if !strings.Contains(err.Error(), "same network") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Validation error tests — explicitMappings section
// ---------------------------------------------------------------------------

func TestNMValidation_Explicit_MissingDR1Key(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - west-cluster: "192.168.110.10"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing dr1 key in explicitMappings")
	}

	if !strings.Contains(err.Error(), "missing cluster key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_InvalidIP(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "not-an-ip"
        west-cluster: "192.168.110.10"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for invalid IP in explicitMappings")
	}

	if !strings.Contains(err.Error(), "non-IPv4") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_IPv6Rejected(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "2001:db8::1"
        west-cluster: "192.168.110.10"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for IPv6 address in explicitMappings")
	}

	if !strings.Contains(err.Error(), "non-IPv4") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_SameIPBothColumns(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.100.10"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error when dr1 IP == dr2 IP (direction ambiguous)")
	}

	if !strings.Contains(err.Error(), "direction ambiguous") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_DuplicateDR1IP(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.110.10"
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.110.20"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for duplicate dr1 IP in explicitMappings")
	}

	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_DuplicateDR2IP(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.110.10"
      - east-cluster: "192.168.100.20"
        west-cluster: "192.168.110.10"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for duplicate dr2 IP in explicitMappings")
	}

	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Explicit_UnknownKey(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: backup
    explicitMappings:
      - east-cluster: "192.168.100.10"
        west-cluster: "192.168.110.10"
        unknown-cluster: "10.0.0.1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for unrecognized key in explicitMappings row")
	}

	if !strings.Contains(err.Error(), "unrecognized key") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Validation error tests — regexMappings section
// ---------------------------------------------------------------------------

func TestNMValidation_Regex_MissingForwardKey(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing forward direction key in regexMappings")
	}

	if !strings.Contains(err.Error(), "is required and must have at least one rule") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Regex_MissingReverseKey(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern:     "^172\\.16\\.100\\.(\\d+)$"
          replacement: "10.20.30.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing reverse direction key in regexMappings")
	}
}

func TestNMValidation_Regex_UnknownDirectionKey(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern:     "^172\\.16\\.100\\.(\\d+)$"
          replacement: "10.20.30.$1"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
      unknown-to-unknown:
        - pattern:     "^1\\.2\\.3\\.(\\d+)$"
          replacement: "4.5.6.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for unrecognized direction key in regexMappings")
	}

	if !strings.Contains(err.Error(), "unrecognized direction key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Regex_InvalidPattern(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern:     "["
          replacement: "10.20.30.$1"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for invalid regex pattern")
	}

	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Regex_MissingPattern(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - replacement: "10.20.30.$1"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing pattern field in regex rule")
	}

	if !strings.Contains(err.Error(), "pattern is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNMValidation_Regex_MissingReplacement(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: mgmt
    regexMappings:
      east-cluster-to-west-cluster:
        - pattern: "^172\\.16\\.100\\.(\\d+)$"
      west-cluster-to-east-cluster:
        - pattern:     "^10\\.20\\.30\\.(\\d+)$"
          replacement: "172.16.100.$1"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing replacement field in regex rule")
	}

	if !strings.Contains(err.Error(), "replacement is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CIDR: IPv4-only validation
// ---------------------------------------------------------------------------

func TestNMValidation_CIDR_IPv6Rejected(t *testing.T) {
	yaml := `
version: "v1"
networkMappings:
  - networkRef:
      nadNamespace: default
      nadName: storage
    cidr:
      east-cluster: "2001:db8::/64"
      west-cluster: "2001:db8:1::/64"
`

	err := mustParseErr(t, yaml)
	if err == nil {
		t.Fatal("expected error for IPv6 CIDRs")
	}

	if !strings.Contains(err.Error(), "only IPv4") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Translation: CIDR block
// ---------------------------------------------------------------------------

func TestNMTranslate_CIDR_Forward(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)
	ref := nadRef("storage")

	got, ruleType, err := translateIP("192.168.100.51", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.120.51" {
		t.Errorf("got %q, want 192.168.120.51", got)
	}

	if ruleType != RuleTypeCIDR {
		t.Errorf("ruleType = %q, want CIDR", ruleType)
	}
}

func TestNMTranslate_CIDR_Reverse(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)
	ref := nadRef("storage")

	got, ruleType, err := translateIP("192.168.120.51", rules, TranslationDirectionReverse, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.100.51" {
		t.Errorf("got %q, want 192.168.100.51", got)
	}

	if ruleType != RuleTypeCIDR {
		t.Errorf("ruleType = %q, want CIDR", ruleType)
	}
}

func TestNMTranslate_CIDR_IPNotInSubnet(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)
	ref := nadRef("storage")

	_, _, err := translateIP("10.0.0.1", rules, TranslationDirectionForward, ref)
	if err == nil {
		t.Error("expected error for IP outside CIDR range")
	}
}

func TestNMTranslate_CIDR_Roundtrip(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)
	ref := nadRef("storage")

	for _, srcIP := range []string{"192.168.100.1", "192.168.100.100", "192.168.100.254"} {
		translated, _, err := translateIP(srcIP, rules, TranslationDirectionForward, ref)
		if err != nil {
			t.Fatalf("%s forward: %v", srcIP, err)
		}

		restored, _, err := translateIP(translated, rules, TranslationDirectionReverse, ref)
		if err != nil {
			t.Fatalf("%s reverse: %v", translated, err)
		}

		if restored != srcIP {
			t.Errorf("roundtrip %q → %q → %q", srcIP, translated, restored)
		}
	}
}

// ---------------------------------------------------------------------------
// Translation: explicit block
// ---------------------------------------------------------------------------

func TestNMTranslate_Explicit_Forward(t *testing.T) {
	rules := mustParse(t, explicitOnlyYAML)
	ref := nadRef("backup")

	got, ruleType, err := translateIP("192.168.100.10", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.120.10" {
		t.Errorf("got %q, want 192.168.120.10", got)
	}

	if ruleType != RuleTypeExplicit {
		t.Errorf("ruleType = %q, want Explicit", ruleType)
	}
}

func TestNMTranslate_Explicit_Reverse(t *testing.T) {
	rules := mustParse(t, explicitOnlyYAML)
	ref := nadRef("backup")

	got, ruleType, err := translateIP("192.168.120.20", rules, TranslationDirectionReverse, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.100.20" {
		t.Errorf("got %q, want 192.168.100.20", got)
	}

	if ruleType != RuleTypeExplicit {
		t.Errorf("ruleType = %q, want Explicit", ruleType)
	}
}

func TestNMTranslate_Explicit_NotFound(t *testing.T) {
	rules := mustParse(t, explicitOnlyYAML)
	ref := nadRef("backup")

	_, _, err := translateIP("192.168.100.99", rules, TranslationDirectionForward, ref)
	if err == nil {
		t.Error("expected error for IP not in explicit table")
	}
}

// ---------------------------------------------------------------------------
// Translation: regex block
// ---------------------------------------------------------------------------

func TestNMTranslate_Regex_Forward(t *testing.T) {
	rules := mustParse(t, regexOnlyYAML)
	ref := nadRef("mgmt")

	got, ruleType, err := translateIP("172.16.100.5", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "10.20.30.5" {
		t.Errorf("got %q, want 10.20.30.5", got)
	}

	if ruleType != RuleTypeRegex {
		t.Errorf("ruleType = %q, want Regex", ruleType)
	}
}

func TestNMTranslate_Regex_Reverse(t *testing.T) {
	rules := mustParse(t, regexOnlyYAML)
	ref := nadRef("mgmt")

	got, ruleType, err := translateIP("10.20.30.5", rules, TranslationDirectionReverse, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "172.16.100.5" {
		t.Errorf("got %q, want 172.16.100.5", got)
	}

	if ruleType != RuleTypeRegex {
		t.Errorf("ruleType = %q, want Regex", ruleType)
	}
}

func TestNMTranslate_Regex_NoMatch(t *testing.T) {
	rules := mustParse(t, regexOnlyYAML)
	ref := nadRef("mgmt")

	_, _, err := translateIP("192.168.1.1", rules, TranslationDirectionForward, ref)
	if err == nil {
		t.Error("expected error when no regex rule matches")
	}
}

func TestNMTranslate_Regex_Roundtrip(t *testing.T) {
	rules := mustParse(t, regexOnlyYAML)
	ref := nadRef("mgmt")
	srcIP := "172.16.100.77"

	translated, _, err := translateIP(srcIP, rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}

	if translated != "10.20.30.77" {
		t.Fatalf("forward: got %q, want 10.20.30.77", translated)
	}

	restored, _, err := translateIP(translated, rules, TranslationDirectionReverse, ref)
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if restored != srcIP {
		t.Errorf("roundtrip %q → %q → %q", srcIP, translated, restored)
	}
}

// ---------------------------------------------------------------------------
// Translation: all-three priority ordering
// ---------------------------------------------------------------------------

// TestNMTranslate_Priority_ExplicitBeforeCIDR verifies that an IP listed in
// explicitMappings uses the explicit override even though the CIDR would also
// match (192.168.100.51 is within 192.168.100.0/24).
func TestNMTranslate_Priority_ExplicitBeforeCIDR(t *testing.T) {
	rules := mustParse(t, allThreeYAML)
	ref := nadRef("backup")

	got, ruleType, err := translateIP("192.168.100.51", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.110.58" {
		t.Errorf("got %q, want 192.168.110.58 (explicit override)", got)
	}

	if ruleType != RuleTypeExplicit {
		t.Errorf("ruleType = %q, want Explicit", ruleType)
	}
}

func TestNMTranslate_Priority_CIDRBeforeRegex(t *testing.T) {
	rules := mustParse(t, allThreeYAML)
	ref := nadRef("backup")

	got, ruleType, err := translateIP("192.168.100.77", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.110.77" {
		t.Errorf("got %q, want 192.168.110.77 (CIDR offset)", got)
	}

	if ruleType != RuleTypeCIDR {
		t.Errorf("ruleType = %q, want CIDR", ruleType)
	}
}

func TestNMTranslate_Priority_RegexFallback(t *testing.T) {
	rules := mustParse(t, allThreeYAML)
	ref := nadRef("backup")

	got, ruleType, err := translateIP("172.16.100.5", rules, TranslationDirectionForward, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "10.20.30.5" {
		t.Errorf("got %q, want 10.20.30.5 (regex fallback)", got)
	}

	if ruleType != RuleTypeRegex {
		t.Errorf("ruleType = %q, want Regex", ruleType)
	}
}

// ---------------------------------------------------------------------------
// Translation: unknown NAD and two-block routing
// ---------------------------------------------------------------------------

func TestNMTranslate_UnknownNAD(t *testing.T) {
	rules := mustParse(t, cidrOnlyYAML)

	_, _, err := translateIP("192.168.100.5", rules, TranslationDirectionForward, nadRef("nonexistent"))
	if err == nil {
		t.Error("expected error for unknown NAD")
	}
}

func TestNMTranslate_TwoBlocks_EachNADUsesOwnBlock(t *testing.T) {
	rules := mustParse(t, twoBlockYAML)

	got, ruleType, err := translateIP("192.168.100.10", rules, TranslationDirectionForward, nadRef("storage"))
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	if got != "192.168.120.10" || ruleType != RuleTypeCIDR {
		t.Errorf("storage CIDR: got %q ruleType %q", got, ruleType)
	}

	got, ruleType, err = translateIP("10.0.0.1", rules, TranslationDirectionForward, nadRef("backup"))
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	if got != "10.0.1.1" || ruleType != RuleTypeExplicit {
		t.Errorf("backup explicit: got %q ruleType %q", got, ruleType)
	}
}

// ---------------------------------------------------------------------------
// translateSourceIP — DRPCInstance integration
// Direction derived from sourceCluster name, not from the IP value.
// ---------------------------------------------------------------------------

func TestNMTranslateSourceIP_NilRules(t *testing.T) {
	d := newDRPCInstanceForTest(nil)

	got := d.translateSourceIP("192.168.100.51", testDR1, nadRef("storage"))
	if got != "192.168.100.51" {
		t.Errorf("expected unchanged IP with nil rules, got %q", got)
	}
}

func TestNMTranslateSourceIP_UnknownSourceCluster(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, cidrOnlyYAML))

	// "other-cluster" is not dr1 or dr2 — must return IP unchanged.
	got := d.translateSourceIP("192.168.100.51", "other-cluster", nadRef("storage"))
	if got != "192.168.100.51" {
		t.Errorf("unknown source cluster: expected unchanged, got %q", got)
	}
}

func TestNMTranslateSourceIP_CIDR_Failover(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, cidrOnlyYAML))

	got := d.translateSourceIP("192.168.100.51", testDR1, nadRef("storage"))
	if got != "192.168.120.51" {
		t.Errorf("CIDR failover: got %q, want 192.168.120.51", got)
	}
}

func TestNMTranslateSourceIP_CIDR_Failback(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, cidrOnlyYAML))

	got := d.translateSourceIP("192.168.120.51", testDR2, nadRef("storage"))
	if got != "192.168.100.51" {
		t.Errorf("CIDR failback: got %q, want 192.168.100.51", got)
	}
}

func TestNMTranslateSourceIP_CIDR_Roundtrip(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, cidrOnlyYAML))
	ref := nadRef("storage")
	original := "192.168.100.77"

	after := d.translateSourceIP(original, testDR1, ref)
	if after != "192.168.120.77" {
		t.Fatalf("failover: got %q, want 192.168.120.77", after)
	}

	back := d.translateSourceIP(after, testDR2, ref)
	if back != original {
		t.Errorf("failback: got %q, want %q", back, original)
	}
}

func TestNMTranslateSourceIP_CIDR_Miss_Unchanged(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, cidrOnlyYAML))

	// 10.0.0.1 is not in the dr1 CIDR — error path, returns unchanged.
	got := d.translateSourceIP("10.0.0.1", testDR1, nadRef("storage"))
	if got != "10.0.0.1" {
		t.Errorf("CIDR miss: expected unchanged, got %q", got)
	}
}

func TestNMTranslateSourceIP_Explicit_Failover(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, explicitOnlyYAML))

	got := d.translateSourceIP("192.168.100.10", testDR1, nadRef("backup"))
	if got != "192.168.120.10" {
		t.Errorf("explicit failover: got %q, want 192.168.120.10", got)
	}
}

func TestNMTranslateSourceIP_Explicit_Failback(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, explicitOnlyYAML))

	got := d.translateSourceIP("192.168.120.10", testDR2, nadRef("backup"))
	if got != "192.168.100.10" {
		t.Errorf("explicit failback: got %q, want 192.168.100.10", got)
	}
}

// TestNMTranslateSourceIP_AllThree_ExplicitOverride is the main integration
// test that matches the design doc log scenario:
//
//	Network: default/backup  sourceCluster: east-cluster
//	inputIP: 192.168.100.51  ruleType: Explicit  outputIP: 192.168.110.58
func TestNMTranslateSourceIP_AllThree_ExplicitOverride(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, allThreeYAML))

	got := d.translateSourceIP("192.168.100.51", testDR1, nadRef("backup"))
	if got != "192.168.110.58" {
		t.Errorf("all-three explicit override: got %q, want 192.168.110.58", got)
	}
}

func TestNMTranslateSourceIP_AllThree_CIDRFallthrough(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, allThreeYAML))

	got := d.translateSourceIP("192.168.100.77", testDR1, nadRef("backup"))
	if got != "192.168.110.77" {
		t.Errorf("all-three CIDR fallthrough: got %q, want 192.168.110.77", got)
	}
}

func TestNMTranslateSourceIP_AllThree_RegexFallthrough(t *testing.T) {
	d := newDRPCInstanceForTest(mustParse(t, allThreeYAML))

	got := d.translateSourceIP("172.16.100.5", testDR1, nadRef("backup"))
	if got != "10.20.30.5" {
		t.Errorf("all-three regex fallthrough: got %q, want 10.20.30.5", got)
	}
}
