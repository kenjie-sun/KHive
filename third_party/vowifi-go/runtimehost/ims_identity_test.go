package runtimehost

import (
	"github.com/1239t/vowifi-go/runtimehost/identity"
	"testing"
)

func TestKHiveIMSPrivateIdentitySeparateFromEAP(t *testing.T) {
	p := &identity.PreparedSession{Profile: identity.Profile{IMSI: "262030000000000", MCC: "262", MNC: "03"}}
	want := "262030000000000@ims.mnc003.mcc262.3gppnetwork.org"
	if got := resolveIMSPrivateID(p, p.Profile.IMSI); got != want {
		t.Fatalf("IMS private identity = %q", got)
	}
	if resolveIMSPrivateID(p, p.Profile.IMSI) == p.EAPIdentity() {
		t.Fatal("EAP NAI leaked into IMS authentication")
	}
	p.IMSIdentity = identity.IMSIdentityInfo{ActualSource: identity.IMSIdentitySourceISIM, IMPI: "isim-user@operator.example", Domain: "operator.example"}
	if got := resolveIMSPrivateID(p, p.Profile.IMSI); got != p.IMSIdentity.IMPI {
		t.Fatal("ISIM identity overridden")
	}
}
