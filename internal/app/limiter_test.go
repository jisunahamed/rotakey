package app

import "testing"

func pointer(value int64) *int64 { return &value }

func TestBuildConstraintsIncludesSharedAndModelPolicies(t *testing.T) {
	credential := credentialRuntime{CredentialView: CredentialView{
		ID:     "key_1",
		Limits: RatePolicy{RPM: pointer(40), TPD: pointer(10000)},
		ModelLimits: map[string]RatePolicy{
			"mdl_1": {RPS: pointer(2), TPR: pointer(1000)},
		},
	}}
	constraints, valid := buildConstraints(credential, "mdl_1", 800)
	if !valid {
		t.Fatal("expected credential to be valid")
	}
	if len(constraints) != 3 {
		t.Fatalf("got %d constraints, want 3", len(constraints))
	}
	if constraints[0].Key != "limit:key_1:all:rpm" {
		t.Fatalf("unexpected first constraint %q", constraints[0].Key)
	}
}

func TestBuildConstraintsRejectsTPR(t *testing.T) {
	credential := credentialRuntime{CredentialView: CredentialView{
		ID: "key_1", Limits: RatePolicy{TPR: pointer(100)},
		ModelLimits: map[string]RatePolicy{},
	}}
	_, valid := buildConstraints(credential, "mdl_1", 101)
	if valid {
		t.Fatal("expected TPR to reject reservation")
	}
}

func TestBuildConstraintsCoversEveryDimension(t *testing.T) {
	limit := pointer(1000)
	credential := credentialRuntime{CredentialView: CredentialView{
		ID: "key_all",
		Limits: RatePolicy{
			RPS: limit, RPM: limit, RPD: limit,
			TPS: limit, TPM: limit, TPD: limit, TPR: limit,
		},
		ModelLimits: map[string]RatePolicy{},
	}}
	constraints, valid := buildConstraints(credential, "mdl_all", 20)
	if !valid {
		t.Fatal("expected policy to accept the request")
	}
	if len(constraints) != 6 {
		t.Fatalf("got %d reservable dimensions, want 6; TPR is an immediate ceiling", len(constraints))
	}
	if constraints[0].WindowMS != 1000 || constraints[1].WindowMS != 60_000 ||
		constraints[2].WindowMS != 86_400_000 {
		t.Fatalf("unexpected request windows: %#v", constraints[:3])
	}
	for _, index := range []int{3, 4, 5} {
		if !constraints[index].Token || constraints[index].Cost != 20 {
			t.Fatalf("constraint %d is not a token reservation: %#v", index, constraints[index])
		}
	}
}

func TestUnlimitedPolicyHasNoConstraints(t *testing.T) {
	credential := credentialRuntime{CredentialView: CredentialView{
		ID: "key_unlimited", ModelLimits: map[string]RatePolicy{},
	}}
	constraints, valid := buildConstraints(credential, "mdl_unlimited", 500)
	if !valid || len(constraints) != 0 {
		t.Fatalf("unlimited credential should be eligible without constraints: valid=%v constraints=%#v", valid, constraints)
	}
}
