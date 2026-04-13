package admin

import (
	"context"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchMedia(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodTalkMode:
		req, err := methods.DecodeTalkModeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TalkMode == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("talk provider not configured")
		}
		out, err := opts.TalkMode(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodBrowserRequest:
		req, err := methods.DecodeBrowserRequestParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.BrowserRequest == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("browser provider not configured")
		}
		out, err := opts.BrowserRequest(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodVoicewakeGet:
		req, err := methods.DecodeVoicewakeGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.VoicewakeGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("voicewake provider not configured")
		}
		out, err := opts.VoicewakeGet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodVoicewakeSet:
		req, err := methods.DecodeVoicewakeSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.VoicewakeSet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("voicewake provider not configured")
		}
		out, err := opts.VoicewakeSet(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSStatus:
		req, err := methods.DecodeTTSStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSStatus == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSStatus(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSProviders:
		req, err := methods.DecodeTTSProvidersParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSProviders == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSProviders(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSSetProvider:
		req, err := methods.DecodeTTSSetProviderParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSSetProvider == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSSetProvider(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSEnable:
		req, err := methods.DecodeTTSEnableParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSEnable == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSEnable(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSDisable:
		req, err := methods.DecodeTTSDisableParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSDisable == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSDisable(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodTTSConvert:
		req, err := methods.DecodeTTSConvertParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TTSConvert == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tts provider not configured")
		}
		out, err := opts.TTSConvert(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
