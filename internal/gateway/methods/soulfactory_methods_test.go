package methods

import "testing"

func TestSoulFactoryMethodsUseRequestReplayPolicy(t *testing.T) {
	for _, method := range SoulFactoryMethods() {
		if got := ControlMethodReplayPolicy(method); got != ControlReplayEventAndRequest {
			t.Fatalf("%s replay policy = %v, want EventAndRequest", method, got)
		}
	}
}

func TestSupportedMethodsIncludesSoulFactoryMethods(t *testing.T) {
	supported := map[string]struct{}{}
	for _, method := range SupportedMethods() {
		supported[method] = struct{}{}
	}
	for _, method := range SoulFactoryMethods() {
		if _, ok := supported[method]; !ok {
			t.Fatalf("%s not found in supported methods", method)
		}
	}
}
