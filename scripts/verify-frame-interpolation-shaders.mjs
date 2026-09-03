#!/usr/bin/env node
import { existsSync, mkdtempSync, rmSync, statSync } from 'node:fs'
import { homedir, platform, tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const shaders = resolve(root, 'src-tauri', 'resources', 'frame-interpolation', 'shaders')
const sdkCandidates = [process.env.ANDROID_SDK_ROOT, process.env.ANDROID_HOME, 'D:\\Software\\Android\\Sdk', join(homedir(), 'Android', 'Sdk')].filter(Boolean)
const sdk = sdkCandidates.find(path => existsSync(join(path, 'ndk', '27.2.12479018', 'shader-tools')))
const glslc = sdk && join(sdk, 'ndk', '27.2.12479018', 'shader-tools', platform() === 'win32' ? 'windows-x86_64' : platform() === 'darwin' ? 'darwin-x86_64' : 'linux-x86_64', platform() === 'win32' ? 'glslc.exe' : 'glslc')
const windowsKit = 'C:\\Program Files (x86)\\Windows Kits\\10\\bin\\10.0.26100.0\\x64\\dxc.exe'
const dxc = process.env.DXC_PATH || windowsKit
if (!glslc || !existsSync(glslc))
  throw new Error('Android NDK glslc is required to validate Vulkan shaders.')
if (platform() === 'win32' && !existsSync(dxc))
  throw new Error('Windows SDK dxc.exe is required to validate DirectML/D3D12 shaders.')

const work = mkdtempSync(join(tmpdir(), 'ohmycine-framegen-shaders-'))
function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8' })
  if (result.status !== 0)
    throw new Error([result.stdout, result.stderr].filter(Boolean).join('\n'))
}

try {
  const outputs = []
  for (const name of ['proxy', 'composite']) {
    const spirv = join(work, `${name}.spv`)
    run(glslc, ['--target-env=vulkan1.1', '-fshader-stage=compute', join(shaders, `${name}.comp`), '-o', spirv])
    outputs.push({ name: `${name}.spv`, bytes: statSync(spirv).size })
    if (platform() === 'win32') {
      const dxil = join(work, `${name}.dxil`)
      run(dxc, ['-T', 'cs_6_2', '-E', 'main', '-enable-16bit-types', join(shaders, `${name}.hlsl`), '-Fo', dxil])
      outputs.push({ name: `${name}.dxil`, bytes: statSync(dxil).size })
    }
  }
  console.log(JSON.stringify({ fp16HdrShadersCompiled: true, outputs }, null, 2))
}
finally {
  rmSync(work, { recursive: true, force: true })
}
