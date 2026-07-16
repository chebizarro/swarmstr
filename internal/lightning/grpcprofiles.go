package lightning

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	FamilyLND  = "lnd"
	FamilyTapd = "tapd"
)

//go:embed descriptors/lnd.pb
var lndDescriptorBytes []byte

//go:embed descriptors/tapd.pb
var tapdDescriptorBytes []byte

//go:embed descriptors/tools.json
var toolManifestBytes []byte

type descriptorManifest struct {
	Version        int                              `json:"version"`
	Upstreams      map[string]upstreamManifest      `json:"upstreams"`
	DescriptorSets map[string]descriptorSetManifest `json:"descriptor_sets"`
	Methods        []curatedMethod                  `json:"methods"`
}

type upstreamManifest struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
	License    string `json:"license"`
}

type descriptorSetManifest struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type curatedMethod struct {
	Family      string `json:"family"`
	Toolset     string `json:"toolset"`
	RPC         string `json:"rpc"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only"`
	Destructive bool   `json:"destructive"`
}

type bundledAssets struct {
	manifest    descriptorManifest
	descriptors map[string]*descriptorpb.FileDescriptorSet
	methods     map[string][]curatedMethod
}

var (
	loadAssetsOnce sync.Once
	loadedAssets   bundledAssets
	loadAssetsErr  error
)

func loadBundledAssets() (bundledAssets, error) {
	loadAssetsOnce.Do(func() {
		if err := json.Unmarshal(toolManifestBytes, &loadedAssets.manifest); err != nil {
			loadAssetsErr = fmt.Errorf("decode Lightning gRPC tool manifest: %w", err)
			return
		}
		if loadedAssets.manifest.Version != 1 {
			loadAssetsErr = fmt.Errorf("unsupported Lightning gRPC tool manifest version %d", loadedAssets.manifest.Version)
			return
		}
		loadedAssets.descriptors = make(map[string]*descriptorpb.FileDescriptorSet, 2)
		loadedAssets.methods = make(map[string][]curatedMethod, 2)
		for family, raw := range map[string][]byte{FamilyLND: lndDescriptorBytes, FamilyTapd: tapdDescriptorBytes} {
			entry, ok := loadedAssets.manifest.DescriptorSets[family]
			if !ok {
				loadAssetsErr = fmt.Errorf("Lightning gRPC tool manifest is missing %s descriptor metadata", family)
				return
			}
			if entry.File != family+".pb" {
				loadAssetsErr = fmt.Errorf("Lightning gRPC tool manifest has unexpected %s descriptor file %q", family, entry.File)
				return
			}
			upstream, ok := loadedAssets.manifest.Upstreams[family]
			if !ok || strings.TrimSpace(upstream.Repository) == "" || strings.TrimSpace(upstream.Tag) == "" ||
				len(strings.TrimSpace(upstream.Commit)) != 40 || strings.TrimSpace(upstream.License) == "" {
				loadAssetsErr = fmt.Errorf("Lightning gRPC tool manifest has incomplete %s upstream provenance", family)
				return
			}
			if _, err := hex.DecodeString(strings.TrimSpace(upstream.Commit)); err != nil {
				loadAssetsErr = fmt.Errorf("Lightning gRPC tool manifest has invalid %s upstream commit", family)
				return
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, strings.TrimSpace(entry.SHA256)) {
				loadAssetsErr = fmt.Errorf("%s descriptor checksum mismatch", family)
				return
			}
			var set descriptorpb.FileDescriptorSet
			if err := proto.Unmarshal(raw, &set); err != nil {
				loadAssetsErr = fmt.Errorf("decode bundled %s descriptor set: %w", family, err)
				return
			}
			if len(set.File) == 0 {
				loadAssetsErr = fmt.Errorf("bundled %s descriptor set is empty", family)
				return
			}
			loadedAssets.descriptors[family] = &set
		}
		loadAssetsErr = validateCuratedManifest(loadedAssets)
	})
	return loadedAssets, loadAssetsErr
}

func validateCuratedManifest(assets bundledAssets) error {
	seenRPC := make(map[string]struct{}, len(assets.manifest.Methods))
	seenTools := make(map[string]struct{}, len(assets.manifest.Methods))
	registries := make(map[string]*protoregistry.Files, len(assets.descriptors))
	for family, set := range assets.descriptors {
		files, err := protodesc.NewFiles(set)
		if err != nil {
			return fmt.Errorf("build bundled %s descriptor registry: %w", family, err)
		}
		registries[family] = files
	}
	for i, method := range assets.manifest.Methods {
		method.Family = strings.ToLower(strings.TrimSpace(method.Family))
		method.Toolset = strings.ToLower(strings.TrimSpace(method.Toolset))
		method.RPC = strings.TrimSpace(method.RPC)
		method.Tool = strings.TrimSpace(method.Tool)
		if method.Family != FamilyLND && method.Family != FamilyTapd {
			return fmt.Errorf("curated method %d has unknown family %q", i, method.Family)
		}
		switch method.Toolset {
		case config.LightningToolsetRead, config.LightningToolsetReceive, config.LightningToolsetSpend, config.LightningToolsetAdmin:
		default:
			return fmt.Errorf("curated method %d has unknown toolset %q", i, method.Toolset)
		}
		if method.RPC == "" || !strings.HasPrefix(method.RPC, "/") {
			return fmt.Errorf("curated method %d has invalid RPC path %q", i, method.RPC)
		}
		if method.Tool == "" || (!strings.HasPrefix(method.Tool, "lnd_") && !strings.HasPrefix(method.Tool, "tap_")) {
			return fmt.Errorf("curated method %s has invalid stable tool name %q", method.RPC, method.Tool)
		}
		rpcKey := method.Family + "\x00" + method.RPC
		if _, duplicate := seenRPC[rpcKey]; duplicate {
			return fmt.Errorf("curated RPC %s is duplicated for %s", method.RPC, method.Family)
		}
		seenRPC[rpcKey] = struct{}{}
		if _, duplicate := seenTools[method.Tool]; duplicate {
			return fmt.Errorf("curated tool name %q is duplicated", method.Tool)
		}
		seenTools[method.Tool] = struct{}{}
		if !descriptorHasMethod(registries[method.Family], method.RPC) {
			return fmt.Errorf("curated RPC %s is absent from bundled %s descriptors", method.RPC, method.Family)
		}
		assets.methods[method.Family] = append(assets.methods[method.Family], method)
	}
	for family := range assets.methods {
		sort.Slice(assets.methods[family], func(i, j int) bool {
			return assets.methods[family][i].RPC < assets.methods[family][j].RPC
		})
	}
	return nil
}

func descriptorHasMethod(files *protoregistry.Files, fullMethod string) bool {
	trimmed := strings.TrimPrefix(strings.TrimSpace(fullMethod), "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash <= 0 || slash == len(trimmed)-1 {
		return false
	}
	serviceName, methodName := trimmed[:slash], trimmed[slash+1:]
	desc, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return false
	}
	service, ok := desc.(protoreflect.ServiceDescriptor)
	return ok && service.Methods().ByName(protoreflect.Name(methodName)) != nil
}

// ValidateBundledDescriptors verifies checksums and every curated manifest RPC.
func ValidateBundledDescriptors() error {
	_, err := loadBundledAssets()
	return err
}

// BundledDescriptorSet returns an isolated copy for dynamic tool and payer use.
func BundledDescriptorSet(family string) (*descriptorpb.FileDescriptorSet, error) {
	assets, err := loadBundledAssets()
	if err != nil {
		return nil, err
	}
	set := assets.descriptors[strings.ToLower(strings.TrimSpace(family))]
	if set == nil {
		return nil, fmt.Errorf("unknown Lightning gRPC descriptor family %q", family)
	}
	return proto.Clone(set).(*descriptorpb.FileDescriptorSet), nil
}

// BuildGRPCEndpointSources builds reflection-free first-class endpoint sources.
func BuildGRPCEndpointSources(cfg config.LightningConfig) ([]toolgrpc.EndpointSource, error) {
	assets, err := loadBundledAssets()
	if err != nil {
		return nil, err
	}
	sources := make([]toolgrpc.EndpointSource, 0, len(cfg.LND.Profiles)+len(cfg.Tapd.Profiles))
	for _, profile := range cfg.LND.Profiles {
		source, err := buildEndpointSource(FamilyLND, profile, assets)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	for _, profile := range cfg.Tapd.Profiles {
		source, err := buildEndpointSource(FamilyTapd, profile, assets)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func buildEndpointSource(family string, profile config.LightningGRPCProfile, assets bundledAssets) (toolgrpc.EndpointSource, error) {
	id := strings.TrimSpace(profile.ID)
	if id == "" {
		return toolgrpc.EndpointSource{}, fmt.Errorf("%s profile id is required", family)
	}
	selected := make(map[string]struct{})
	for _, toolset := range profile.EffectiveToolsets() {
		selected[strings.ToLower(strings.TrimSpace(toolset))] = struct{}{}
	}
	if len(selected) == 0 {
		selected[config.LightningToolsetRead] = struct{}{}
	}

	names := make(map[string]string)
	traits := make(map[string]agent.ToolTraits)
	descriptions := make(map[string]string)
	for _, method := range assets.methods[family] {
		if _, ok := selected[method.Toolset]; !ok {
			continue
		}
		names[method.RPC] = method.Tool
		traits[method.RPC] = agent.ToolTraits{
			ConcurrencySafe:   true,
			ReadOnly:          method.ReadOnly,
			Destructive:       method.Destructive,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		}
		descriptions[method.RPC] = method.Description
	}
	if len(names) == 0 {
		return toolgrpc.EndpointSource{}, fmt.Errorf("%s profile %q selects no curated RPCs", family, id)
	}
	set := assets.descriptors[family]
	if set == nil {
		return toolgrpc.EndpointSource{}, errors.New("bundled descriptor family is unavailable")
	}

	exposure := profile.Exposure
	exposure.Namespace = ""
	exposure.IncludeServices = nil
	exposure.ExcludeMethods = nil
	internalID := family + ":" + id
	return toolgrpc.EndpointSource{
		Profile: config.GRPCEndpointConfig{
			ID:     internalID,
			Target: strings.TrimSpace(profile.Target),
			Transport: config.GRPCTransportConfig{
				TLSMode:    config.GRPCTransportTLSModeCustomCA,
				CAFile:     strings.TrimSpace(profile.TLSCertFile),
				ServerName: strings.TrimSpace(profile.ServerName),
			},
			Defaults: profile.Defaults,
			Exposure: exposure,
		},
		DescriptorSet:    proto.Clone(set).(*descriptorpb.FileDescriptorSet),
		ToolNames:        names,
		ToolTraits:       traits,
		ToolDescriptions: descriptions,
		MetadataSources: map[string]toolgrpc.CredentialSource{
			"macaroon": {
				Ref:      strings.TrimSpace(profile.Macaroon.Ref),
				Encoding: profile.Macaroon.EffectiveEncoding(),
			},
		},
	}, nil
}
