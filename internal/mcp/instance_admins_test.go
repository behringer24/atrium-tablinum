package mcpserver

import (
	"strings"
	"testing"
)

// The instance admin right can be granted and revoked through admin_instance_admins. Accounts in
// bootstrap_admins keep it whatever is done: they are the way back in.
func TestGrantAndRevokeInstanceAdmin(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	anna := e.session(t, "anna@x.de")
	carl := e.session(t, "carl@x.de")
	// An account exists after its first login.
	e.mustCall(t, boss, "list_spaces", nil)
	e.mustCall(t, anna, "list_spaces", nil)
	e.mustCall(t, carl, "list_spaces", nil)

	emails := func(out map[string]any) map[string]string {
		got := map[string]string{}
		for _, a := range out["admins"].([]any) {
			m := a.(map[string]any)
			got[m["email"].(string)] = m["source"].(string)
		}
		return got
	}
	grant := func(email string) (map[string]any, string) {
		return e.call(t, boss, "admin_instance_admins", map[string]any{"action": "grant", "user_email": email, "confirm": true})
	}

	if got := emails(e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "list"})); len(got) != 1 || got["boss@x.de"] != "config" {
		t.Fatalf("list at the start: %v", got)
	}

	// Without a confirmation nothing happens; a stranger and a nonsense action are refused.
	if _, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "grant", "user_email": "anna@x.de"}); !strings.Contains(msg, "confirm=true") {
		t.Errorf("grant without confirm: %q", msg)
	}
	if _, msg := grant("nobody@x.de"); !strings.Contains(msg, "unknown") {
		t.Errorf("grant to an unknown user: %q", msg)
	}
	if _, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "promote"}); !strings.Contains(msg, "list, grant or revoke") {
		t.Errorf("unknown action: %q", msg)
	}
	if toolNames(t, e, e.session(t, "anna@x.de"))["admin_audit_log"] {
		t.Fatal("anna has admin tools before the grant")
	}

	// Granted: the account is an instance admin from its next connection on.
	out, msg := grant("anna@x.de")
	if msg != "" || emails(out)["anna@x.de"] != "granted" || emails(out)["boss@x.de"] != "config" {
		t.Fatalf("grant: %q %v", msg, out)
	}
	if again, _ := grant("anna@x.de"); !strings.Contains(again["message"].(string), "already") {
		t.Errorf("second grant: %v", again)
	}
	anna = e.session(t, "anna@x.de")
	if !toolNames(t, e, anna)["admin_audit_log"] {
		t.Fatal("anna has no admin tools after the grant")
	}
	if got := emails(e.mustCall(t, anna, "admin_instance_admins", map[string]any{"action": "list"})); len(got) != 2 {
		t.Errorf("list for a granted admin: %v", got)
	}

	// A granted admin can grant too, but cannot take away a right from the configuration or from itself.
	e.mustCall(t, anna, "admin_instance_admins", map[string]any{"action": "grant", "user_email": "carl@x.de", "confirm": true})
	for email, want := range map[string]string{"boss@x.de": "configuration", "anna@x.de": "Your own"} {
		if _, msg := e.call(t, anna, "admin_instance_admins", map[string]any{"action": "revoke", "user_email": email, "confirm": true}); !strings.Contains(msg, want) {
			t.Errorf("revoke %s: %q", email, msg)
		}
	}
	if _, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "revoke", "user_email": "boss@x.de", "confirm": true}); !strings.Contains(msg, "configuration") {
		t.Errorf("revoke the right of the configuration: %q", msg)
	}

	// Revoked: the tools disappear and a call is refused at once, even on the old connection.
	if _, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "revoke", "user_email": "carl@x.de"}); !strings.Contains(msg, "confirm=true") {
		t.Errorf("revoke without confirm: %q", msg)
	}
	out = e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "revoke", "user_email": "carl@x.de", "confirm": true})
	if _, ok := emails(out)["carl@x.de"]; ok {
		t.Errorf("revoke: %v", out)
	}
	if toolNames(t, e, e.session(t, "carl@x.de"))["admin_audit_log"] {
		t.Error("a revoked admin still sees admin tools")
	}
	if again := e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "revoke", "user_email": "carl@x.de", "confirm": true}); !strings.Contains(again["message"].(string), "not an instance admin") {
		t.Errorf("second revoke: %v", again)
	}

	// Both changes are in the audit log.
	for _, action := range []string{"admin_grant_instance_admin", "admin_revoke_instance_admin"} {
		log := e.mustCall(t, boss, "admin_audit_log", map[string]any{"action": action})
		if n := len(log["entries"].([]any)); n == 0 {
			t.Errorf("no audit entry for %s", action)
		}
	}
}

// Offboarding takes a granted admin right away, so reactivating the account does not bring it back;
// a locked account cannot be granted the right.
func TestOffboardingRemovesAGrantedAdminRight(t *testing.T) {
	e := setup(t, 0)
	boss := e.session(t, "boss@x.de")
	carl := e.session(t, "carl@x.de")
	e.mustCall(t, boss, "list_spaces", nil)
	e.mustCall(t, carl, "list_spaces", nil)

	e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "grant", "user_email": "carl@x.de", "confirm": true})
	out := e.mustCall(t, boss, "admin_offboard_user", map[string]any{"user_email": "carl@x.de", "confirm": true})
	if !strings.Contains(out["message"].(string), "instance admin right was removed") {
		t.Errorf("offboarding message: %v", out["message"])
	}
	if _, msg := e.call(t, boss, "admin_instance_admins", map[string]any{"action": "grant", "user_email": "carl@x.de", "confirm": true}); !strings.Contains(msg, "locked") {
		t.Errorf("grant to a locked account: %q", msg)
	}
	e.mustCall(t, boss, "admin_reactivate_user", map[string]any{"user_email": "carl@x.de"})
	list := e.mustCall(t, boss, "admin_instance_admins", map[string]any{"action": "list"})
	if n := len(list["admins"].([]any)); n != 1 {
		t.Errorf("after reactivation: %v", list["admins"])
	}
}
