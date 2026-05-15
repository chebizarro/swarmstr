package methods

import "testing"

func TestSoulFactoryMethodsIncludesCustomizationSurface(t *testing.T) {
	methods := map[string]struct{}{}
	for _, method := range SoulFactoryMethods() {
		methods[method] = struct{}{}
	}
	for _, method := range []string{
		MethodSoulFactoryAvatarGenerate,
		MethodSoulFactoryAvatarSet,
		MethodSoulFactoryVoiceConfigure,
		MethodSoulFactoryVoiceSample,
		MethodSoulFactoryMemoryConfigure,
		MethodSoulFactoryMemoryReindex,
		MethodSoulFactoryPersonaUpdate,
		MethodSoulFactoryConfigReload,
	} {
		if _, ok := methods[method]; !ok {
			t.Fatalf("%s not registered as a SoulFactory method", method)
		}
		if !IsSoulFactoryMethod(method) {
			t.Fatalf("%s not recognized as a SoulFactory method", method)
		}
	}
}

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
