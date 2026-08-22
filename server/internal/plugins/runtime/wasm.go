package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	CodeInvalidModule       = "plugin_runtime_module_invalid"
	CodeCapabilityDenied    = "plugin_runtime_capability_denied"
	CodeStartFailed         = "plugin_runtime_start_failed"
	CodeStartTimeout        = "plugin_runtime_start_timeout"
	CodeUnavailable         = "plugin_runtime_unavailable"
	CodeOperationInvalid    = "plugin_runtime_operation_invalid"
	CodeResponseInvalid     = "plugin_runtime_response_invalid"
	CodeResponseTooLarge    = "plugin_runtime_response_too_large"
	defaultCallTimeout      = 2 * time.Second
	defaultMemoryLimitPage  = 1024 // 64 MiB
	maxRequestBytes         = 256 * 1024
	maxResponseBytes        = 4 * 1024 * 1024
	maxHostCallRequestBytes = 4 * 1024 * 1024
	hostModuleName          = "ohmycine"
	hostCallExportName      = "host_call"
)

const (
	HostCallInvalid        int32 = -1
	HostCallDenied         int32 = -2
	HostCallOutputTooSmall int32 = -3
	HostCallFailed         int32 = -4
)

// Operation codes are part of the public runtime v1 ABI. Keep these values in
// sync with plugin-sdk/src/runtime.ts and the cross-language fixture.
var operationCodes = map[string]uint64{
	"site.navigation":        1,
	"site.feed":              2,
	"site.search":            3,
	"site.detail":            4,
	"media.playback":         5,
	"media.download_plan":    6,
	"site.history":           7,
	"playback.progress_sync": 8,
}

type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return CodeUnavailable
}

// Host is the minimal first-generation WASM sandbox. It deliberately does not
// instantiate WASI or any OhMyCine host module, so plugins have no filesystem,
// network, environment, clock, randomness, credential or process capability.
// Those APIs will be added individually behind Manifest permissions.
type Host struct {
	mu      sync.Mutex
	runtime wazero.Runtime
	modules map[string]*runningModule
	api     CapabilityHost
}

// CapabilityHost is the only bridge from untrusted guest code into Server
// services. The implementation must repeat all permission checks using the
// host-bound plugin ID; guest payload fields never select another identity.
type CapabilityHost interface {
	Call(context.Context, string, uint32, []byte) ([]byte, error)
}

type runningModule struct {
	module api.Module
	mu     sync.Mutex
}

func NewHost(ctx context.Context) *Host {
	config := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(defaultMemoryLimitPage).
		WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false)
	host := &Host{runtime: wazero.NewRuntimeWithConfig(ctx, config), modules: make(map[string]*runningModule)}
	_, _ = host.runtime.NewHostModuleBuilder(hostModuleName).
		NewFunctionBuilder().WithFunc(host.hostCall).Export(hostCallExportName).
		Instantiate(ctx)
	return host
}

func (host *Host) SetCapabilityHost(api CapabilityHost) {
	host.mu.Lock()
	host.api = api
	host.mu.Unlock()
}

func (host *Host) Validate(ctx context.Context, entryPath string) error {
	module, err := host.instantiate(ctx, entryPath, "", false)
	if err != nil {
		return err
	}
	return module.Close(context.Background())
}

func (host *Host) Start(ctx context.Context, pluginID, entryPath string, generation uint64) error {
	if pluginID == "" || generation == 0 {
		return &Error{Code: CodeInvalidModule, Cause: errors.New("plugin runtime identity is invalid")}
	}
	name := fmt.Sprintf("%s@%d", pluginID, generation)
	module, err := host.instantiate(ctx, entryPath, name, true)
	if err != nil {
		return err
	}
	host.mu.Lock()
	previous := host.modules[pluginID]
	host.modules[pluginID] = &runningModule{module: module}
	host.mu.Unlock()
	if previous != nil {
		_ = previous.module.Close(context.Background())
	}
	return nil
}

// Invoke calls the versioned JSON ABI exposed by an already started plugin.
// Inputs and outputs are copied at the sandbox boundary and never retain guest
// memory. The packed i64 result uses the high 32 bits for pointer and low 32
// bits for length, which is deterministic across SDK languages.
func (host *Host) Invoke(ctx context.Context, pluginID, operation string, request []byte) ([]byte, error) {
	code, ok := operationCodes[operation]
	if !ok {
		return nil, &Error{Code: CodeOperationInvalid, Cause: errors.New("unknown plugin operation")}
	}
	if len(request) > maxRequestBytes {
		return nil, &Error{Code: CodeResponseTooLarge, Cause: errors.New("plugin request is too large")}
	}
	host.mu.Lock()
	running := host.modules[pluginID]
	host.mu.Unlock()
	if running == nil {
		return nil, &Error{Code: CodeUnavailable, Cause: errors.New("plugin is not running")}
	}
	running.mu.Lock()
	defer running.mu.Unlock()
	module := running.module
	allocate := module.ExportedFunction("omc_alloc")
	invoke := module.ExportedFunction("omc_invoke")
	free := module.ExportedFunction("omc_free")
	if allocate == nil || invoke == nil {
		return nil, &Error{Code: CodeCapabilityDenied, Cause: errors.New("plugin does not expose the invocation ABI")}
	}
	callContext, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	allocated, err := allocate.Call(callContext, uint64(len(request)))
	if err != nil || len(allocated) != 1 {
		return nil, callError(callContext, CodeResponseInvalid, err)
	}
	pointer := uint32(allocated[0])
	if len(request) > 0 && !module.Memory().Write(pointer, request) {
		return nil, &Error{Code: CodeResponseInvalid, Cause: errors.New("plugin request allocation is outside guest memory")}
	}
	result, err := invoke.Call(callContext, code, uint64(pointer), uint64(len(request)))
	if err != nil || len(result) != 1 {
		return nil, callError(callContext, CodeResponseInvalid, err)
	}
	responsePointer, responseLength := uint32(result[0]>>32), uint32(result[0])
	if responseLength > maxResponseBytes {
		return nil, &Error{Code: CodeResponseTooLarge, Cause: errors.New("plugin response is too large")}
	}
	response, ok := module.Memory().Read(responsePointer, responseLength)
	if !ok {
		return nil, &Error{Code: CodeResponseInvalid, Cause: errors.New("plugin response is outside guest memory")}
	}
	copied := append([]byte(nil), response...)
	if free != nil {
		_, _ = free.Call(callContext, uint64(responsePointer), uint64(responseLength))
		if responsePointer != pointer || responseLength != uint32(len(request)) {
			_, _ = free.Call(callContext, uint64(pointer), uint64(len(request)))
		}
	}
	return copied, nil
}

func (host *Host) Stop(pluginID string) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	running := host.modules[pluginID]
	if running == nil {
		return nil
	}
	if err := running.module.Close(context.Background()); err != nil {
		// A plugin that cannot be closed must not be allowed to keep executing
		// behind a disabled/uninstalled database state. Closing the whole host is
		// the fail-closed fallback; the Server will require a restart before any
		// plugin can run again.
		_ = host.runtime.Close(context.Background())
		host.modules = make(map[string]*runningModule)
		return &Error{Code: CodeUnavailable, Cause: err}
	}
	delete(host.modules, pluginID)
	return nil
}

func (host *Host) Close(ctx context.Context) error {
	host.mu.Lock()
	host.modules = make(map[string]*runningModule)
	host.mu.Unlock()
	return host.runtime.Close(ctx)
}

func (host *Host) instantiate(ctx context.Context, entryPath, name string, start bool) (api.Module, error) {
	wasm, err := os.ReadFile(entryPath)
	if err != nil {
		return nil, &Error{Code: CodeInvalidModule, Cause: err}
	}
	callContext, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	compiled, err := host.runtime.CompileModule(callContext, wasm)
	if err != nil {
		return nil, &Error{Code: CodeInvalidModule, Cause: err}
	}
	defer compiled.Close(context.Background())
	if len(compiled.ImportedMemories()) != 0 {
		return nil, &Error{Code: CodeCapabilityDenied, Cause: errors.New("plugin imports memory")}
	}
	for _, imported := range compiled.ImportedFunctions() {
		moduleName, functionName, importedFunction := imported.Import()
		if !importedFunction || moduleName != hostModuleName || functionName != hostCallExportName || len(imported.ParamTypes()) != 5 || imported.ParamTypes()[0] != api.ValueTypeI32 || imported.ParamTypes()[1] != api.ValueTypeI32 || imported.ParamTypes()[2] != api.ValueTypeI32 || imported.ParamTypes()[3] != api.ValueTypeI32 || imported.ParamTypes()[4] != api.ValueTypeI32 || len(imported.ResultTypes()) != 1 || imported.ResultTypes()[0] != api.ValueTypeI32 {
			return nil, &Error{Code: CodeCapabilityDenied, Cause: errors.New("plugin imports an unavailable host capability")}
		}
	}
	apiVersionDefinition, ok := compiled.ExportedFunctions()["omc_api_version"]
	if !ok || len(apiVersionDefinition.ParamTypes()) != 0 || len(apiVersionDefinition.ResultTypes()) != 1 || apiVersionDefinition.ResultTypes()[0] != api.ValueTypeI32 {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin omc_api_version ABI is missing or invalid")}
	}
	if startDefinition, exists := compiled.ExportedFunctions()["omc_start"]; exists && (len(startDefinition.ParamTypes()) != 0 || len(startDefinition.ResultTypes()) != 0) {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin omc_start ABI is invalid")}
	}
	if allocateDefinition, exists := compiled.ExportedFunctions()["omc_alloc"]; exists && (len(allocateDefinition.ParamTypes()) != 1 || allocateDefinition.ParamTypes()[0] != api.ValueTypeI32 || len(allocateDefinition.ResultTypes()) != 1 || allocateDefinition.ResultTypes()[0] != api.ValueTypeI32) {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin omc_alloc ABI is invalid")}
	}
	if invokeDefinition, exists := compiled.ExportedFunctions()["omc_invoke"]; exists && (len(invokeDefinition.ParamTypes()) != 3 || invokeDefinition.ParamTypes()[0] != api.ValueTypeI32 || invokeDefinition.ParamTypes()[1] != api.ValueTypeI32 || invokeDefinition.ParamTypes()[2] != api.ValueTypeI32 || len(invokeDefinition.ResultTypes()) != 1 || invokeDefinition.ResultTypes()[0] != api.ValueTypeI64) {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin omc_invoke ABI is invalid")}
	}
	if freeDefinition, exists := compiled.ExportedFunctions()["omc_free"]; exists && (len(freeDefinition.ParamTypes()) != 2 || freeDefinition.ParamTypes()[0] != api.ValueTypeI32 || freeDefinition.ParamTypes()[1] != api.ValueTypeI32 || len(freeDefinition.ResultTypes()) != 0) {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin omc_free ABI is invalid")}
	}
	_, hasAllocate := compiled.ExportedFunctions()["omc_alloc"]
	_, hasInvoke := compiled.ExportedFunctions()["omc_invoke"]
	if hasAllocate != hasInvoke {
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin invocation ABI is incomplete")}
	}
	// Never invoke a conventional WASI _start export. OhMyCine calls only its
	// reviewed ABI exports explicitly after the import and signature checks.
	config := wazero.NewModuleConfig().WithName(name).WithStartFunctions()
	module, err := host.runtime.InstantiateModule(callContext, compiled, config)
	if err != nil {
		if errors.Is(callContext.Err(), context.DeadlineExceeded) {
			return nil, &Error{Code: CodeStartTimeout, Cause: err}
		}
		return nil, &Error{Code: CodeStartFailed, Cause: err}
	}
	version, err := module.ExportedFunction("omc_api_version").Call(callContext)
	if err != nil || len(version) != 1 || uint32(version[0]) != 1 {
		_ = module.Close(context.Background())
		return nil, &Error{Code: CodeInvalidModule, Cause: errors.New("plugin API version probe failed")}
	}
	if start {
		if function := module.ExportedFunction("omc_start"); function != nil {
			if _, err := function.Call(callContext); err != nil {
				_ = module.Close(context.Background())
				if errors.Is(callContext.Err(), context.DeadlineExceeded) {
					return nil, &Error{Code: CodeStartTimeout, Cause: err}
				}
				return nil, &Error{Code: CodeStartFailed, Cause: err}
			}
		}
	}
	return module, nil
}

func (host *Host) hostCall(ctx context.Context, module api.Module, operation, requestPointer, requestLength, responsePointer, responseCapacity uint32) int32 {
	if requestLength > maxHostCallRequestBytes || responseCapacity > maxResponseBytes || module == nil || module.Memory() == nil {
		return HostCallInvalid
	}
	request, ok := module.Memory().Read(requestPointer, requestLength)
	if !ok {
		return HostCallInvalid
	}
	name := module.Name()
	separator := strings.LastIndexByte(name, '@')
	if separator <= 0 {
		return HostCallDenied
	}
	pluginID := name[:separator]
	host.mu.Lock()
	capabilityHost := host.api
	host.mu.Unlock()
	if capabilityHost == nil {
		return HostCallDenied
	}
	response, err := capabilityHost.Call(ctx, pluginID, operation, append([]byte(nil), request...))
	if err != nil {
		var denied interface{ PermissionDenied() bool }
		if errors.As(err, &denied) && denied.PermissionDenied() {
			return HostCallDenied
		}
		return HostCallFailed
	}
	if len(response) > maxResponseBytes {
		return HostCallFailed
	}
	if uint32(len(response)) > responseCapacity {
		return HostCallOutputTooSmall
	}
	if len(response) > 0 && !module.Memory().Write(responsePointer, response) {
		return HostCallInvalid
	}
	return int32(len(response))
}

func callError(ctx context.Context, code string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: CodeStartTimeout, Cause: err}
	}
	if err == nil {
		err = errors.New("plugin call returned an invalid result")
	}
	return &Error{Code: code, Cause: err}
}
