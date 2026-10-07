package config

import (
	"testing"
)

func TestDeriveSni(t *testing.T) {
	ca := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	jwt := "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----"

	sni := DeriveSni(ca, jwt)
	expected := "bbea5a76e5e41e393f7b8eabd6ba88e4.54697dda92.app"
	if sni != expected {
		t.Fatalf("expected SNI %s, got %s", expected, sni)
	}
}
