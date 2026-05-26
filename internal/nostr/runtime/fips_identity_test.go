package runtime

import "testing"

func TestFIPSIPv6FromPubkey_validationVectors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pubkeyHex   string
		wantNodeHex string
		wantIPv6    string
	}{
		{
			name:        "generator",
			pubkeyHex:   "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
			wantNodeHex: "132f39a98c31baaddba6525f5d43f295",
			wantIPv6:    "fd13:2f39:a98c:31ba:addb:a652:5f5d:43f2",
		},
		{
			name:        "compressed-generator-xonly-pair",
			pubkeyHex:   "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5",
			wantNodeHex: "0135da2f8acf7b9e3090939432e47684",
			wantIPv6:    "fd01:35da:2f8a:cf7b:9e30:9093:9432:e476",
		},
		{
			name:        "validation-sample",
			pubkeyHex:   "84bf7562262bbd6940085748f3be6afa52ae317155181ece31b66351ccffa4b0",
			wantNodeHex: "69e08d65cc3a6b9c2c2ac4bd405e4b0e",
			wantIPv6:    "fd69:e08d:65cc:3a6b:9c2c:2ac4:bd40:5e4b",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip, err := FIPSIPv6FromPubkey(tc.pubkeyHex)
			if err != nil {
				t.Fatalf("FIPSIPv6FromPubkey: %v", err)
			}
			if got := ip.String(); got != tc.wantIPv6 {
				t.Fatalf("IPv6 mismatch: got %s, want %s (node_addr %s)", got, tc.wantIPv6, tc.wantNodeHex)
			}
		})
	}
}

func TestFIPSIPv6FromPubkey_rejectsCompressedPubkey(t *testing.T) {
	compressed := "02" + "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	if _, err := FIPSIPv6FromPubkey(compressed); err == nil {
		t.Fatal("expected compressed 33-byte pubkey to be rejected")
	}
}

func TestFIPSAddrString_usesCanonicalDerivation(t *testing.T) {
	addr, err := FIPSAddrString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", 1337)
	if err != nil {
		t.Fatalf("FIPSAddrString: %v", err)
	}
	if want := "[fd13:2f39:a98c:31ba:addb:a652:5f5d:43f2]:1337"; addr != want {
		t.Fatalf("address mismatch: got %s, want %s", addr, want)
	}
}
