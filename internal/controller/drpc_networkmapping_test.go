// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers //nolint:testpackage

// Unit tests for drpc_networkmapping.go.
// Run with: go test ./internal/controller/ -run 'TestTranslate|TestParse|TestExplicit|TestPattern'

import (
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeMappingsConfigMap(name, ns, yaml string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string]string{"mappings.yaml": yaml},
	}
}

// ---------------------------------------------------------------------------
// Parse: pattern only
// ---------------------------------------------------------------------------

const patternOnlyYAML = `
patternTranslation:
  forwardPattern:      "^172\\.16\\.0\\.(\\d+)$"
  forwardReplacement:  "192.168.20.$1"
  reversePattern:      "^192\\.168\\.20\\.(\\d+)$"
  reverseReplacement:  "172.16.0.$1"
`

func TestParseMappingsYAML_Pattern(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if rules.PatternTranslation == nil {
		t.Fatal("PatternTranslation is nil")
	}

	if rules.PatternTranslation.ForwardPattern != `^172\.16\.0\.(\d+)$` {
		t.Errorf("ForwardPattern = %q", rules.PatternTranslation.ForwardPattern)
	}

	if rules.PatternTranslation.ForwardReplacement != "192.168.20.$1" {
		t.Errorf("ForwardReplacement = %q", rules.PatternTranslation.ForwardReplacement)
	}

	if rules.PatternTranslation.ReversePattern != `^192\.168\.20\.(\d+)$` {
		t.Errorf("ReversePattern = %q", rules.PatternTranslation.ReversePattern)
	}

	if rules.PatternTranslation.ReverseReplacement != "172.16.0.$1" {
		t.Errorf("ReverseReplacement = %q", rules.PatternTranslation.ReverseReplacement)
	}

	if len(rules.ExplicitMappings) != 0 {
		t.Errorf("expected 0 explicit mappings, got %d", len(rules.ExplicitMappings))
	}
}

// ---------------------------------------------------------------------------
// Parse: explicit only
// ---------------------------------------------------------------------------

const explicitOnlyYAML = `
explicitMappings:
  - sourceIP: "172.16.0.10"
    destIP:   "192.168.20.100"
  - sourceIP: "172.16.0.20"
    destIP:   "192.168.20.200"
`

func TestParseMappingsYAML_Explicit(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", explicitOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if rules.PatternTranslation != nil {
		t.Error("PatternTranslation should be nil when only explicitMappings is present")
	}

	if len(rules.ExplicitMappings) != 2 {
		t.Fatalf("expected 2 explicit mappings, got %d", len(rules.ExplicitMappings))
	}

	if rules.ExplicitMappings[0].SourceIP != "172.16.0.10" {
		t.Errorf("[0].SourceIP = %q", rules.ExplicitMappings[0].SourceIP)
	}

	if rules.ExplicitMappings[0].DestIP != "192.168.20.100" {
		t.Errorf("[0].DestIP = %q", rules.ExplicitMappings[0].DestIP)
	}

	if rules.ExplicitMappings[1].SourceIP != "172.16.0.20" {
		t.Errorf("[1].SourceIP = %q", rules.ExplicitMappings[1].SourceIP)
	}
}

// ---------------------------------------------------------------------------
// Parse: explicit overrides + pattern fallback
// ---------------------------------------------------------------------------

const patternWithOverridesYAML = `
patternTranslation:
  forwardPattern:      "^172\\.16\\.0\\.(\\d+)$"
  forwardReplacement:  "192.168.20.$1"
  reversePattern:      "^192\\.168\\.20\\.(\\d+)$"
  reverseReplacement:  "172.16.0.$1"

explicitMappings:
  - sourceIP: "172.16.0.10"
    destIP:   "192.168.20.100"
`

func TestParseMappingsYAML_PatternWithOverrides(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternWithOverridesYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if rules.PatternTranslation == nil {
		t.Fatal("PatternTranslation is nil")
	}

	if len(rules.ExplicitMappings) != 1 {
		t.Fatalf("expected 1 explicit mapping, got %d", len(rules.ExplicitMappings))
	}
}

// ---------------------------------------------------------------------------
// Parse: missing data key
// ---------------------------------------------------------------------------

func TestParseMappingsYAML_MissingKey(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "ns"},
		Data:       map[string]string{"other": "data"},
	}

	_, err := ParseNetworkMappingConfigMap(cm)
	if err == nil {
		t.Error("expected error for missing mappings.yaml key, got nil")
	}
}

// ---------------------------------------------------------------------------
// Validation errors
// ---------------------------------------------------------------------------

func TestValidation_EmptyMappings(t *testing.T) {
	// Neither patternTranslation nor explicitMappings present.
	cm := makeMappingsConfigMap("x", "ns", `someOtherKey: value`)

	_, err := ParseNetworkMappingConfigMap(cm)
	if err == nil {
		t.Error("expected error: no patternTranslation or explicitMappings")
	}
}

func TestValidation_ExplicitMissingEntries(t *testing.T) {
	// explicitMappings key present but empty list.
	cm := makeMappingsConfigMap("x", "ns", `
explicitMappings: []
`)

	_, err := ParseNetworkMappingConfigMap(cm)
	if err == nil {
		t.Error("expected error: empty explicitMappings list")
	}
}

func TestValidation_PatternMissingBlock(t *testing.T) {
	// patternTranslation present but all fields empty.
	cm := makeMappingsConfigMap("x", "ns", `
patternTranslation:
  forwardPattern: ""
  forwardReplacement: ""
  reversePattern: ""
  reverseReplacement: ""
`)

	_, err := ParseNetworkMappingConfigMap(cm)
	if err == nil {
		t.Error("expected error: patternTranslation with empty forwardPattern")
	}
}

// ---------------------------------------------------------------------------
// TranslateIP: method=pattern (forward and reverse)
// ---------------------------------------------------------------------------

func TestTranslateIP_Pattern_Forward(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("172.16.0.55", rules, TranslationDirectionForward)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.20.55" {
		t.Errorf("forward: got %q, want 192.168.20.55", got)
	}
}

func TestTranslateIP_Pattern_Reverse(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("192.168.20.55", rules, TranslationDirectionReverse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "172.16.0.55" {
		t.Errorf("reverse: got %q, want 172.16.0.55", got)
	}
}

func TestTranslateIP_Pattern_NoMatch(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	_, err = mgr.TranslateIP("10.0.0.1", rules, TranslationDirectionForward)
	if err == nil {
		t.Error("expected error for IP that doesn't match pattern, got nil")
	}
}

func TestTranslateIP_Pattern_Roundtrip(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	for _, srcIP := range []string{"172.16.0.1", "172.16.0.100", "172.16.0.254"} {
		translated, err := mgr.TranslateIP(srcIP, rules, TranslationDirectionForward)
		if err != nil {
			t.Fatalf("forward %q: %v", srcIP, err)
		}

		restored, err := mgr.TranslateIP(translated, rules, TranslationDirectionReverse)
		if err != nil {
			t.Fatalf("reverse %q: %v", translated, err)
		}

		if restored != srcIP {
			t.Errorf("roundtrip %q → %q → %q (want %q)", srcIP, translated, restored, srcIP)
		}
	}
}

// ---------------------------------------------------------------------------
// TranslateIP: method=explicit (forward and reverse)
// ---------------------------------------------------------------------------

func TestTranslateIP_Explicit_Forward(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", explicitOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("172.16.0.10", rules, TranslationDirectionForward)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.20.100" {
		t.Errorf("forward: got %q, want 192.168.20.100", got)
	}
}

func TestTranslateIP_Explicit_Reverse(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", explicitOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	// Reverse: destIP → sourceIP
	got, err := mgr.TranslateIP("192.168.20.100", rules, TranslationDirectionReverse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "172.16.0.10" {
		t.Errorf("reverse: got %q, want 172.16.0.10", got)
	}
}

func TestTranslateIP_Explicit_NotFound(t *testing.T) {
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", explicitOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	_, err = mgr.TranslateIP("172.16.0.99", rules, TranslationDirectionForward)
	if err == nil {
		t.Error("expected error for IP not in explicit table, got nil")
	}
}

// ---------------------------------------------------------------------------
// TranslateIP: method=pattern-with-overrides
// ---------------------------------------------------------------------------

func TestTranslateIP_PatternWithOverrides_ExplicitHit(t *testing.T) {
	// 172.16.0.10 has an explicit override → 192.168.20.100 (not pattern result 192.168.20.10)
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternWithOverridesYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("172.16.0.10", rules, TranslationDirectionForward)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.20.100" {
		t.Errorf("explicit override: got %q, want 192.168.20.100", got)
	}
}

func TestTranslateIP_PatternWithOverrides_PatternFallback(t *testing.T) {
	// 172.16.0.50 not in explicit table → pattern applies → 192.168.20.50
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternWithOverridesYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("172.16.0.50", rules, TranslationDirectionForward)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "192.168.20.50" {
		t.Errorf("pattern fallback: got %q, want 192.168.20.50", got)
	}
}

func TestTranslateIP_PatternWithOverrides_Reverse_ExplicitHit(t *testing.T) {
	// Reverse direction: 192.168.20.100 → 172.16.0.10 via explicit table
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternWithOverridesYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("192.168.20.100", rules, TranslationDirectionReverse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "172.16.0.10" {
		t.Errorf("reverse explicit: got %q, want 172.16.0.10", got)
	}
}

func TestTranslateIP_PatternWithOverrides_Reverse_PatternFallback(t *testing.T) {
	// Reverse direction: 192.168.20.77 not in explicit table → reverse pattern → 172.16.0.77
	cm := makeMappingsConfigMap("ip-translation", "ramen-system", patternWithOverridesYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	mgr := NewDRPCNetworkMappingManager(nil, logr.Discard())

	got, err := mgr.TranslateIP("192.168.20.77", rules, TranslationDirectionReverse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "172.16.0.77" {
		t.Errorf("reverse pattern fallback: got %q, want 172.16.0.77", got)
	}
}

// ---------------------------------------------------------------------------
// directionForIP + translateSourceIP tests
// ---------------------------------------------------------------------------

// newDRPCInstanceForTest builds a minimal DRPCInstance for unit tests.
func newDRPCInstanceForTest(rules *NetworkMappingRules) *DRPCInstance {
	return &DRPCInstance{
		reconciler:          &DRPlacementControlReconciler{},
		log:                 logr.Discard(),
		instance:            &rmn.DRPlacementControl{},
		networkMappingRules: rules,
	}
}

// ConfigMap YAML for the 192.168.100.x ↔ 192.168.200.x scenario.
const subnet100to200YAML = `
patternTranslation:
  forwardPattern:     "^192\\.168\\.100\\.(\\d+)$"
  forwardReplacement: "192.168.200.$1"
  reversePattern:     "^192\\.168\\.200\\.(\\d+)$"
  reverseReplacement: "192.168.100.$1"
`

// TestDirectionForIP_Pattern: direction is resolved from the IP itself,
// not from any cluster-name field.
func TestDirectionForIP_Pattern(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", subnet100to200YAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	d := newDRPCInstanceForTest(rules)

	if dir := d.directionForIP("192.168.100.5"); dir != TranslationDirectionForward {
		t.Errorf("100.x: expected Forward, got %v", dir)
	}

	if dir := d.directionForIP("192.168.200.5"); dir != TranslationDirectionReverse {
		t.Errorf("200.x: expected Reverse, got %v", dir)
	}

	// IP matching neither pattern defaults to Forward (translateIP will error).
	if dir := d.directionForIP("10.0.0.1"); dir != TranslationDirectionForward {
		t.Errorf("unknown: expected Forward default, got %v", dir)
	}
}

// TestDirectionForIP_Explicit: direction is found by checking which table column
// the IP belongs to.
func TestDirectionForIP_Explicit(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", explicitOnlyYAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	d := newDRPCInstanceForTest(rules)

	if dir := d.directionForIP("172.16.0.10"); dir != TranslationDirectionForward {
		t.Errorf("sourceIP: expected Forward, got %v", dir)
	}

	if dir := d.directionForIP("192.168.20.100"); dir != TranslationDirectionReverse {
		t.Errorf("destIP: expected Reverse, got %v", dir)
	}
}

// TestTranslateSourceIP_NilRules: no-op pass-through when no ConfigMap is set.
func TestTranslateSourceIP_NilRules(t *testing.T) {
	d := newDRPCInstanceForTest(nil)

	got := d.translateSourceIP("192.168.100.51")
	if got != "192.168.100.51" {
		t.Errorf("expected unchanged IP, got %q", got)
	}
}

// TestTranslateSourceIP_Failover: IP from the forward subnet → translated forward.
func TestTranslateSourceIP_Failover(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", subnet100to200YAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d := newDRPCInstanceForTest(rules)

	got := d.translateSourceIP("192.168.100.51")
	if got != "192.168.200.51" {
		t.Errorf("failover: got %q, want 192.168.200.51", got)
	}
}

// TestTranslateSourceIP_Failback: IP from the reverse subnet → translated reverse.
// This is the exact scenario from the error log:
// primaryCluster=dr2 had 192.168.200.51 — should become 192.168.100.51.
func TestTranslateSourceIP_Failback(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", subnet100to200YAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d := newDRPCInstanceForTest(rules)

	got := d.translateSourceIP("192.168.200.51")
	if got != "192.168.100.51" {
		t.Errorf("failback: got %q, want 192.168.100.51", got)
	}
}

// TestTranslateSourceIP_Roundtrip: failover→failback returns original IP.
func TestTranslateSourceIP_Roundtrip(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", subnet100to200YAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d := newDRPCInstanceForTest(rules)
	original := "192.168.100.77"

	afterFailover := d.translateSourceIP(original)
	if afterFailover != "192.168.200.77" {
		t.Fatalf("failover: got %q, want 192.168.200.77", afterFailover)
	}

	afterFailback := d.translateSourceIP(afterFailover)
	if afterFailback != original {
		t.Errorf("failback: got %q, want %q", afterFailback, original)
	}
}

// TestTranslateSourceIP_PatternMiss: IP matching neither pattern is returned
// unchanged (error logged, no abort).
func TestTranslateSourceIP_PatternMiss(t *testing.T) {
	cm := makeMappingsConfigMap("ip-map", "ns", subnet100to200YAML)

	rules, err := ParseNetworkMappingConfigMap(cm)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	d := newDRPCInstanceForTest(rules)

	got := d.translateSourceIP("10.0.0.1")
	if got != "10.0.0.1" {
		t.Errorf("pattern miss: expected unchanged IP, got %q", got)
	}
}
