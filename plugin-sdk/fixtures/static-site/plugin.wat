(module
  ;; Source fixture for the public copied-JSON ABI. The build script injects
  ;; deterministic response data and compiles this shape without WASI/imports.
  (memory (export "memory") 2)
  (global $heap (mut i32) (i32.const 65536))
  (func (export "omc_api_version") (result i32) i32.const 1)
  (func (export "omc_alloc") (param $size i32) (result i32)
    (local $pointer i32)
    global.get $heap
    local.tee $pointer
    local.get $size
    i32.add
    global.set $heap
    local.get $pointer)
  ;; omc_invoke is generated from contract.v1.json by build-installable-fixture.mjs.
)
