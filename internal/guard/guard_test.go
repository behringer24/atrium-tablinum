package guard

import (
	"errors"
	"strings"
	"testing"
)

func kinds(text string) []string {
	var k []string
	for _, f := range Scan(text) {
		k = append(k, f.Kind)
	}
	return k
}

func TestRejectsSecrets(t *testing.T) {
	cases := map[string]string{
		"private key":        "-----BEGIN RSA PRIVATE KEY-----\nMIIE",
		"AWS access key":     "key AKIAIOSFODNN7EXAMPLE1"[:4] + "AKIAIOSFODNN7ABCDEFG",
		"GitHub token":       "token ghp_" + strings.Repeat("a1B2", 9),
		"Slack token":        "xoxb-1234567890-abcdefghij",
		"Google API key":     "AIza" + strings.Repeat("a1_-", 8) + "abc",
		"JSON Web Token":     "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop",
		"bearer token":       "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
		"credentials in URL": "postgres://app:S3cr3tPw@db.internal:5432/x",
		"password or secret": "Das Passwort: Tr0ub4dor&3 gilt für den Server",
		"IBAN":               "Konto DE89 3704 0044 0532 0130 00 bitte nutzen",
		"card number":        "Visa 4111 1111 1111 1111 läuft ab",
	}
	for kind, text := range cases {
		t.Run(kind, func(t *testing.T) {
			got := kinds(text)
			found := false
			for _, k := range got {
				found = found || k == kind
			}
			if !found {
				t.Errorf("%q not detected (found: %v)", kind, got)
			}
			if Check(text) == nil {
				t.Error("Check must return an error")
			}
		})
	}
}

func TestAcceptsOrdinaryText(t *testing.T) {
	for _, text := range []string{
		"# Onboarding\nNeue Mitarbeiter erhalten ihr Passwort per Post.",
		"Passwort: vertraulich behandeln",
		"Das Token wird alle 24 Stunden erneuert.",
		"password: <dein-passwort>",
		"password = ${DB_PASSWORD}",
		"api_key: xxxxxxxxxxxx",
		"postgres://user:${PASS}@host/db",
		"https://example.com/pfad?x=1",
		"Bestellnummer 4929 0000 0000 0001 wurde versandt", // Luhn-invalid
		"Kundennummer 1234567890123456",
		"DE00 0000 0000 0000 0000 00", // mod-97-invalid
		"Telefon +49 170 1234567, PLZ 80331",
		"commit 3f786850e387550fdab836ed7e6dc881de23001b",
	} {
		if f := Scan(text); len(f) > 0 {
			t.Errorf("%q wrongly rejected: %v", text, f)
		}
	}
}

func TestFindingsNeverContainTheSecret(t *testing.T) {
	secret := "Tr0ub4dor&3xyz"
	err := Check("Line 1\npassword: " + secret)
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatal("not an *Error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Error("error message contains the secret")
	}
	if ge.Findings[0].Line != 2 {
		t.Errorf("line = %d", ge.Findings[0].Line)
	}
}

func TestIBANValidation(t *testing.T) {
	for iban, want := range map[string]bool{
		"DE89370400440532013000":      true,
		"GB82 WEST 1234 5698 7654 32": true,
		"DE89370400440532013001":      false,
		"DE8937040044053201":          false, // too short
	} {
		if got := validIBAN(iban); got != want {
			t.Errorf("%s: %v", iban, got)
		}
	}
}

func TestCardValidation(t *testing.T) {
	for card, want := range map[string]bool{
		"4111 1111 1111 1111": true,
		"5555-5555-5555-4444": true,
		"3782 822463 10005":   true,
		"4111 1111 1111 1112": false, // Luhn
		"1234 5678 9012 3452": false, // Luhn matches, issuer not plausible
	} {
		if got := validCard(card); got != want {
			t.Errorf("%s: %v", card, got)
		}
	}
}
