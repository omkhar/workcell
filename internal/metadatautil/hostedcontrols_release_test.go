// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import "testing"

func TestSummarizeReleaseProtectionPreservesPermissiveDecoding(t *testing.T) {
	firstAdmin := map[string]any{"type": "admin_bypass", "enabled": false, "id": "first-admin"}
	lastAdmin := map[string]any{"type": "admin_bypass", "enabled": true, "id": "last-admin"}
	firstReviewer := map[string]any{"type": "required_reviewers", "reviewers": []any{}, "id": "first-reviewer"}
	lastReviewer := map[string]any{"type": "required_reviewers", "reviewers": []any{"owner"}, "id": "last-reviewer"}
	environment := map[string]any{"protection_rules": []any{
		"malformed",
		map[string]any{"type": "unknown"},
		firstReviewer,
		firstAdmin,
		lastReviewer,
		lastAdmin,
	}}
	summary := summarizeReleaseProtection(environment)
	if len(summary.reviewerRules) != 2 || summary.reviewerRules[0]["id"] != "first-reviewer" || summary.reviewerRules[1]["id"] != "last-reviewer" {
		t.Fatalf("reviewer rules = %#v, want both rules in input order", summary.reviewerRules)
	}
	if summary.adminBypassRule["id"] != "last-admin" {
		t.Fatalf("admin bypass rule = %#v, want last rule", summary.adminBypassRule)
	}
}

func TestVerifyReleaseEnvironmentControlsPreservesFailurePrecedence(t *testing.T) {
	validCollaborator := []any{releaseTestCollaborator("owner", true)}
	tests := []struct {
		name          string
		mode          string
		environment   map[string]any
		repoMeta      map[string]any
		collaborators []any
		ownerLogin    string
		ownerType     string
		want          string
	}{
		{
			name: "review gate before admin bypass", mode: "review-gated",
			environment: releaseTestEnvironment(true),
			want:        "release environment on omkhar/workcell must require a human reviewer",
		},
		{
			name: "empty reviewers before admin bypass", mode: "review-gated",
			environment: releaseTestEnvironment(true, releaseTestReviewer(false, false)),
			want:        "release environment on omkhar/workcell must define at least one reviewer",
		},
		{
			name: "plan visibility before reviewer gates", mode: "plan-limited-private",
			environment: releaseTestEnvironment(false, releaseTestReviewer(true, false)),
			repoMeta:    map[string]any{"private": false},
			want:        "release environment mode 'plan-limited-private' on omkhar/workcell is only valid for private repositories",
		},
		{
			name: "public visibility before identity", mode: "single-owner-public",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": true},
			ownerType: "Organization", want: "release environment mode 'single-owner-public' on omkhar/workcell is only valid for public repositories",
		},
		{
			name: "public owner before collaborators", mode: "single-owner-public",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": false},
			ownerType: "Organization", want: "release environment mode 'single-owner-public' on omkhar/workcell is only valid for user-owned repositories",
		},
		{
			name: "collaborator count before reviewers", mode: "single-owner-public",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": false}, ownerLogin: "owner", ownerType: "User",
			want: "release environment mode 'single-owner-public' on omkhar/workcell requires exactly one direct collaborator",
		},
		{
			name: "collaborator login before reviewers", mode: "single-owner-public",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": false}, collaborators: []any{releaseTestCollaborator("other", true)}, ownerLogin: "owner", ownerType: "User",
			want: "release environment mode 'single-owner-public' on omkhar/workcell requires the owner to be the only direct collaborator",
		},
		{
			name: "collaborator admin before reviewers", mode: "single-owner-public",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": false}, collaborators: []any{releaseTestCollaborator("owner", false)}, ownerLogin: "owner", ownerType: "User",
			want: "release environment mode 'single-owner-public' on omkhar/workcell requires the owner to retain admin permission",
		},
		{
			name: "reviewer gate before admin bypass", mode: "single-owner-public",
			environment: releaseTestEnvironment(true), repoMeta: map[string]any{"private": false}, collaborators: validCollaborator, ownerLogin: "owner", ownerType: "User",
			want: "release environment on omkhar/workcell must define a reviewer gate in single-owner-public mode",
		},
		{
			name: "self review before aggregate reviewer", mode: "single-owner-public",
			environment: releaseTestEnvironment(false, releaseTestReviewer(false, true), releaseTestReviewer(true, false)), repoMeta: map[string]any{"private": false}, collaborators: validCollaborator, ownerLogin: "owner", ownerType: "User",
			want: "release environment on omkhar/workcell must allow self-review in single-owner-public mode",
		},
		{
			name: "aggregate reviewer before admin bypass", mode: "single-owner-public",
			environment: releaseTestEnvironment(true, releaseTestReviewer(false, false)), repoMeta: map[string]any{"private": false}, collaborators: validCollaborator, ownerLogin: "owner", ownerType: "User",
			want: "release environment on omkhar/workcell must define at least one reviewer in single-owner-public mode",
		},
		{
			name: "admin bypass after valid reviewer", mode: "single-owner-public",
			environment: releaseTestEnvironment(true, releaseTestReviewer(true, false)), repoMeta: map[string]any{"private": false}, collaborators: validCollaborator, ownerLogin: "owner", ownerType: "User",
			want: "release environment on omkhar/workcell must not allow administrator bypass",
		},
		{
			name: "private visibility before identity", mode: "single-owner-private",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": false}, ownerType: "Organization",
			want: "release environment mode 'single-owner-private' on omkhar/workcell is only valid for private repositories",
		},
		{
			name: "private owner before collaborators", mode: "single-owner-private",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": true}, ownerType: "Organization",
			want: "release environment mode 'single-owner-private' on omkhar/workcell is only valid for user-owned repositories",
		},
		{
			name: "private collaborator identity", mode: "single-owner-private",
			environment: releaseTestEnvironment(false), repoMeta: map[string]any{"private": true}, ownerLogin: "owner", ownerType: "User",
			want: "release environment mode 'single-owner-private' on omkhar/workcell requires exactly one direct collaborator",
		},
	}
	for _, test := range tests {
		inputs := hostedControlInputs{
			releaseEnvironment:  test.environment,
			repoMeta:            test.repoMeta,
			directCollaborators: test.collaborators,
		}
		err := verifyReleaseEnvironmentControls(test.mode, inputs, test.ownerLogin, test.ownerType, "omkhar/workcell")
		if err == nil || err.Error() != test.want {
			t.Errorf("%s: error = %v, want %q", test.name, err, test.want)
		}
	}
}

func releaseTestEnvironment(adminBypass bool, rules ...map[string]any) map[string]any {
	protectionRules := make([]any, 0, len(rules)+1)
	for _, rule := range rules {
		protectionRules = append(protectionRules, rule)
	}
	protectionRules = append(protectionRules, map[string]any{"type": "admin_bypass", "enabled": adminBypass})
	return map[string]any{"protection_rules": protectionRules, "can_admins_bypass": adminBypass}
}

func releaseTestReviewer(hasReviewer, preventSelfReview bool) map[string]any {
	reviewers := []any{}
	if hasReviewer {
		reviewers = append(reviewers, "owner")
	}
	return map[string]any{"type": "required_reviewers", "reviewers": reviewers, "prevent_self_review": preventSelfReview}
}

func releaseTestCollaborator(login string, admin bool) map[string]any {
	return map[string]any{"login": login, "permissions": map[string]any{"admin": admin}}
}
