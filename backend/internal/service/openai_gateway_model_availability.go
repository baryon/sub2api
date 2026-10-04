package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// DiagnoseModelAvailabilityForPlatform reports whether the requested model
// is configured to be served by any persistently eligible OpenAI-compatible
// account in the group for the given platform (e.g. PlatformOpenAI,
// PlatformGrok). The platform scopes the candidate pool so distinct
// OpenAI-compatible platforms do not cross-contaminate diagnosis results.
// The query bypasses scheduler snapshots and ignores transient runtime state.
//
// Safe to call on the error path: returns {true,true} on any internal
// failure or when the inputs preclude meaningful diagnosis (empty model,
// nil service), so callers stay on the 503 fallback branch.
func (s *OpenAIGatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	platform = NormalizeOpenAICompatiblePlatform(platform)
	queryGroupID := groupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(
		ctx,
		queryGroupID,
		[]string{platform},
		includeGrouped,
	)
	if err != nil {
		// Conservative fallback so the caller keeps returning 503; we do not
		// want a transient lookup failure to flip into 404 model_not_found.
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		// Mirrors the per-candidate filter used during account selection
		// (openai_account_scheduler.isAccountRequestCompatible): empty
		// model_mapping accepts everything; otherwise the explicit / wildcard
		// mapping must match.
		if accounts[i].IsModelSupported(requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}

// OtohaBilledModels lists, in a stable order and once each, the names usage is billed as when a persistently eligible
// account of the platform in the group serves the requested model: the account's model mapping as Forward resolves
// it (passthrough accounts bill the model as requested). claimedBy, when set, keeps only the accounts whose mapping
// names that model, as the scheduler does for a model a composite group routes by account ownership.
func (s *OpenAIGatewayService) OtohaBilledModels(ctx context.Context, groupID *int64, requestedModel, platform, claimedBy string) ([]string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if s == nil || s.accountRepo == nil || requestedModel == "" {
		return nil, nil
	}
	queryGroupID := groupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, queryGroupID, []string{NormalizeOpenAICompatiblePlatform(platform)}, includeGrouped)
	if err != nil {
		return nil, err
	}
	var billed []string
	for i := range accounts {
		if !accounts[i].IsModelSupported(requestedModel) {
			continue
		}
		if claimedBy != "" && !explicitModelMappingClaims(accounts[i], claimedBy) {
			continue
		}
		name, _ := resolveOpenAIForwardMappedModels(&accounts[i], requestedModel, false)
		if !otohaContains(billed, name) {
			billed = append(billed, name)
		}
	}
	sort.Strings(billed)
	return billed, nil
}
