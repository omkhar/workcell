package metadatautil

import "testing"

func bypassActor(actorType, mode string, id float64) any {
	entry := map[string]any{"actor_type": actorType, "bypass_mode": mode}
	if id != 0 {
		entry["actor_id"] = id
	}
	return entry
}

func bypassControls(review, status []any) hostedRulesetControls {
	return hostedRulesetControls{
		branchIntegrity:    map[string]any{"bypass_actors": []any{}},
		branchReview:       map[string]any{"name": "review", "bypass_actors": review},
		branchStatusChecks: map[string]any{"name": "status", "bypass_actors": status},
		tagRelease:         map[string]any{"name": "tags", "bypass_actors": []any{bypassActor("RepositoryRole", "always", 5)}},
	}
}

func TestVerifyHostedRulesetBypassesUpstreamRefreshApp(t *testing.T) {
	role := bypassActor("RepositoryRole", "pull_request", 5)
	app := bypassActor("Integration", "pull_request", 42)
	tests := []struct {
		name    string
		review  []any
		status  []any
		want    int
		wantErr bool
	}{
		{"no app", []any{role}, nil, 0, false},
		{"one app", []any{role, app}, nil, 0, false},
		{"one app matching policy id", []any{app}, nil, 42, false},
		{"one app mismatching policy id", []any{app}, nil, 43, true},
		{"second app", []any{app, bypassActor("Integration", "pull_request", 43)}, nil, 0, true},
		{"wrong mode", []any{bypassActor("Integration", "always", 42)}, nil, 0, true},
		{"missing actor id", []any{bypassActor("Integration", "pull_request", 0)}, nil, 0, true},
		{"other actor type", []any{bypassActor("Team", "pull_request", 42)}, nil, 0, true},
		{"app on status ruleset", []any{role}, []any{app}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyHostedRulesetBypasses(bypassControls(tt.review, tt.status), tt.want, "o/r")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	integrity := bypassControls([]any{role}, nil)
	integrity.branchIntegrity = map[string]any{"bypass_actors": []any{app}}
	if err := verifyHostedRulesetBypasses(integrity, 0, "o/r"); err == nil {
		t.Fatal("app on integrity ruleset must fail")
	}
}
