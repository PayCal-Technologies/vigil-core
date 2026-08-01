package plugins

import (
	"fmt"
	"slices"
	"strings"
)

type Policy struct {
	Mode                   string   `json:"mode"`
	Local                  string   `json:"local"`
	RequireSigned          bool     `json:"require_signed"`
	MinSignatureThreshold  int      `json:"min_signature_threshold,omitempty"`
	AllowedIDs             []string `json:"allowed_ids"`
	DeniedIDs              []string `json:"denied_ids"`
	AllowedPublisherKeyIDs []string `json:"allowed_publisher_key_ids"`
	DeniedCapabilities     []string `json:"denied_capabilities"`
}

func DefaultPolicy() Policy {
	return Policy{
		Mode:                   "enabled",
		Local:                  "allow",
		RequireSigned:          false,
		AllowedIDs:             []string{},
		DeniedIDs:              []string{},
		AllowedPublisherKeyIDs: []string{},
		DeniedCapabilities:     []string{},
	}
}

func ValidatePolicy(policy Policy) error {
	switch policy.Mode {
	case "enabled", "disabled":
	default:
		return fmt.Errorf("mode must be enabled or disabled")
	}
	switch policy.Local {
	case "allow", "deny":
	default:
		return fmt.Errorf("local must be allow or deny")
	}
	if policy.RequireSigned && policy.Local != "deny" {
		return fmt.Errorf("require_signed requires local=deny")
	}
	if policy.MinSignatureThreshold < 0 {
		return fmt.Errorf("min_signature_threshold must not be negative")
	}
	if policy.AllowedIDs == nil || policy.DeniedIDs == nil || policy.AllowedPublisherKeyIDs == nil || policy.DeniedCapabilities == nil {
		return fmt.Errorf("plugin policy list fields must be arrays")
	}
	if err := validateUniqueStrings("allowed_ids", policy.AllowedIDs, func(value string) bool {
		return pluginIDPattern.MatchString(value)
	}); err != nil {
		return err
	}
	if err := validateUniqueStrings("denied_ids", policy.DeniedIDs, func(value string) bool {
		return pluginIDPattern.MatchString(value)
	}); err != nil {
		return err
	}
	for _, id := range policy.AllowedIDs {
		if slices.Contains(policy.DeniedIDs, id) {
			return fmt.Errorf("plugin %s cannot be both allowed and denied", id)
		}
	}
	if err := validateUniqueStrings("allowed_publisher_key_ids", policy.AllowedPublisherKeyIDs, validDigest); err != nil {
		return err
	}
	allowedCapabilities := map[string]bool{
		"filesystem:read": true, "filesystem:write": true,
		"git:read": true, "git:write": true, "network": true,
		"process": true, "environment": true, "secrets": true, "interactive": true,
	}
	if err := validateUniqueStrings("denied_capabilities", policy.DeniedCapabilities, func(value string) bool {
		return allowedCapabilities[value]
	}); err != nil {
		return err
	}
	return nil
}

func CheckPolicy(
	policy Policy,
	id string,
	capabilities []string,
	acquisition string,
	publisherKeyIDs []string,
	signatureThreshold int,
) error {
	if err := ValidatePolicy(policy); err != nil {
		return wrapPluginError(ErrorInvalid, "validate plugin policy", err)
	}
	if err := CheckAcquisitionPolicy(policy, acquisition); err != nil {
		return err
	}
	if len(policy.AllowedIDs) > 0 && !slices.Contains(policy.AllowedIDs, id) {
		return pluginError(ErrorBlocked, "enforce plugin policy", "plugin %s is not allowed", id)
	}
	if slices.Contains(policy.DeniedIDs, id) {
		return pluginError(ErrorBlocked, "enforce plugin policy", "plugin %s is denied", id)
	}
	if acquisition == "signed-index" && policy.MinSignatureThreshold > 0 && signatureThreshold < policy.MinSignatureThreshold {
		return pluginError(ErrorBlocked, "enforce plugin policy", "publisher signature threshold %d is below policy minimum %d", signatureThreshold, policy.MinSignatureThreshold)
	}
	if acquisition == "signed-index" && len(policy.AllowedPublisherKeyIDs) > 0 {
		allowed := 0
		for _, keyID := range publisherKeyIDs {
			if slices.Contains(policy.AllowedPublisherKeyIDs, keyID) {
				allowed++
			}
		}
		if allowed < signatureThreshold {
			return pluginError(ErrorBlocked, "enforce plugin policy", "allowed publisher signatures are below the required threshold")
		}
	}
	for _, capability := range capabilities {
		if slices.Contains(policy.DeniedCapabilities, capability) {
			return pluginError(ErrorBlocked, "enforce plugin policy", "capability %s is denied", capability)
		}
	}
	return nil
}

func CheckAcquisitionPolicy(policy Policy, acquisition string) error {
	if err := ValidatePolicy(policy); err != nil {
		return wrapPluginError(ErrorInvalid, "validate plugin policy", err)
	}
	if policy.Mode == "disabled" {
		return pluginError(ErrorBlocked, "enforce plugin policy", "plugins are disabled")
	}
	if acquisition == "local" && (policy.Local == "deny" || policy.RequireSigned) {
		return pluginError(ErrorBlocked, "enforce plugin policy", "local plugin acquisition is denied")
	}
	return nil
}

func NormalizePolicy(policy *Policy) (Policy, error) {
	if policy == nil {
		return DefaultPolicy(), nil
	}
	normalized := *policy
	normalized.Mode = strings.TrimSpace(normalized.Mode)
	normalized.Local = strings.TrimSpace(normalized.Local)
	normalized.AllowedIDs = append([]string{}, normalized.AllowedIDs...)
	normalized.DeniedIDs = append([]string{}, normalized.DeniedIDs...)
	normalized.AllowedPublisherKeyIDs = append([]string{}, normalized.AllowedPublisherKeyIDs...)
	normalized.DeniedCapabilities = append([]string{}, normalized.DeniedCapabilities...)
	if err := ValidatePolicy(normalized); err != nil {
		return Policy{}, err
	}
	return normalized, nil
}
