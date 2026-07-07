package models

import "testing"

// TestRole_CanQueueOps verifies the DESIGN/02 §2.1.1 capability model: Manager
// holds CapQueueOps natively, but SystemAdmin does NOT get it just by
// outranking Manager via AtLeast - that's the whole point of splitting queue
// ownership into an exact-membership capability instead of a rank check.
func TestRole_CanQueueOps(t *testing.T) {
	cases := []struct {
		role Role
		want bool
	}{
		{RoleCustomer, false},
		{RoleEngineer, false},
		{RoleManager, true},
		{RoleSystemAdmin, false},
		{RoleAgent, false},
	}
	for _, c := range cases {
		if got := c.role.Can(CapQueueOps); got != c.want {
			t.Errorf("%s.Can(CapQueueOps) = %v, want %v", c.role, got, c.want)
		}
	}
}

func TestRole_CanSudoAndUserAdmin(t *testing.T) {
	for _, cap := range []Capability{CapSudo, CapUserAdmin} {
		if !RoleSystemAdmin.Can(cap) {
			t.Errorf("SystemAdmin.Can(%s) = false, want true", cap)
		}
		for _, r := range []Role{RoleCustomer, RoleEngineer, RoleManager, RoleAgent} {
			if r.Can(cap) {
				t.Errorf("%s.Can(%s) = true, want false", r, cap)
			}
		}
	}
}

// TestRole_CanAgentDetect verifies only RoleAgent may backdate
// Ticket.DetectedAt (DESIGN/03 §3.1.2b) - anyone else doing so would corrupt MTTD.
func TestRole_CanAgentDetect(t *testing.T) {
	if !RoleAgent.Can(CapAgentDetect) {
		t.Error("Agent.Can(CapAgentDetect) = false, want true")
	}
	for _, r := range []Role{RoleCustomer, RoleEngineer, RoleManager, RoleSystemAdmin} {
		if r.Can(CapAgentDetect) {
			t.Errorf("%s.Can(CapAgentDetect) = true, want false", r)
		}
	}
}

// TestRole_AtLeastStillMonotonic guards the general staff-seniority ordering
// (used for checks unrelated to queue ownership) even after the rename.
func TestRole_AtLeastStillMonotonic(t *testing.T) {
	order := []Role{RoleCustomer, RoleEngineer, RoleManager, RoleSystemAdmin}
	for i, r := range order {
		for j, min := range order {
			want := i >= j
			if got := r.AtLeast(min); got != want {
				t.Errorf("%s.AtLeast(%s) = %v, want %v", r, min, got, want)
			}
		}
	}
}

func TestRole_IsAgent(t *testing.T) {
	agents := map[Role]bool{
		RoleCustomer:    false,
		RoleEngineer:    true,
		RoleManager:     true,
		RoleSystemAdmin: true,
		RoleAgent:       true,
	}
	for r, want := range agents {
		if got := r.IsAgent(); got != want {
			t.Errorf("%s.IsAgent() = %v, want %v", r, got, want)
		}
	}
}

// TestRole_AgentTiesEngineerRank verifies RoleAgent shares Engineer's rank
// (DESIGN/08 §8.1: same baseline staff surface), so it passes the same
// AtLeast(Engineer) gates that Pickup/notes/labels/runbooks use.
func TestRole_AgentTiesEngineerRank(t *testing.T) {
	if !RoleAgent.AtLeast(RoleEngineer) {
		t.Error("Agent.AtLeast(Engineer) = false, want true")
	}
	if !RoleEngineer.AtLeast(RoleAgent) {
		t.Error("Engineer.AtLeast(Agent) = false, want true")
	}
	if RoleAgent.AtLeast(RoleManager) {
		t.Error("Agent.AtLeast(Manager) = true, want false")
	}
}
