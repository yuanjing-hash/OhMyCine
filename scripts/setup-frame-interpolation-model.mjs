#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { copyFileSync, createWriteStream, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { basename, dirname, join, relative, resolve } from 'node:path'
import process from 'node:process'
import { spawnSync } from 'node:child_process'
import { pipeline } from 'node:stream/promises'
import { fileURLToPath } from 'node:url'
import SevenZip from '7z-wasm'

const rootDir = fileURLToPath(new URL('..', import.meta.url))
const upstreamCommit = 'a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7'
const upstreamBase = `https://raw.githubusercontent.com/nihui/rife-ncnn-vulkan/${upstreamCommit}`
const modelVersion = 'rife-v4.6'
const officialRifeCommit = 'f6b5132517695127bdb5d5a8c3727e719f0fda22'
const officialRifeArchive = {
  name: 'practical-rife-v4.6.zip',
  url: 'https://drive.usercontent.google.com/download?id=1EAbsfY7mjnXNa6RAsATj2ImAEqmHTjbE&export=download&confirm=t',
  sha256: '52b094d14cf275e925a5ae25381e46f94fab1c232a847dc45117cfd7c89ceec2',
  checkpointSha256: '008646e761f0e67cb77f0c6c44cfe3c3e5a05d9d9465311b9681ca650ce030db',
  outputSha256: '067f1eb525cebb0f3d737aac9ca26425e6fad3cdf9afffcb674bd8b62aa03a54',
}
const officialRifeLicense = {
  name: 'LICENSE-Practical-RIFE',
  url: `https://raw.githubusercontent.com/hzwer/Practical-RIFE/${officialRifeCommit}/LICENSE`,
  sha256: '7932fb49341512b959b1744a6d9cbb39e5a1ec89da438a34d0454d5d8df9fecd',
}
const modelFiles = [
  {
    name: 'flownet.param',
    url: `${upstreamBase}/models/${modelVersion}/flownet.param`,
    sha256: '724569596bcd1e7b9fa50455c604777ebed99746d2ef40aa86e31b5725f1053c',
  },
  {
    name: 'flownet.bin',
    url: `${upstreamBase}/models/${modelVersion}/flownet.bin`,
    sha256: 'f334ed2260149ce0188a6dcf049844e8b0cdd912e01cbcfb63553157d2508958',
  },
  {
    name: 'LICENSE',
    url: `${upstreamBase}/LICENSE`,
    sha256: 'a73beab18143600af0b10c6050a953ec233775ce31c2bf3373a794db535329fd',
  },
]
const resourceRoot = resolve(rootDir, 'src-tauri', 'resources', 'frame-interpolation')
const modelDir = join(resourceRoot, 'models', modelVersion)
const androidAssetRoot = resolve(rootDir, 'src-tauri', 'gen', 'android', 'app', 'src', 'main', 'assets', 'frame-interpolation')
const maxAttempts = 3

function ensureInside(parent, target) {
  const rel = relative(parent, target)
  if (!rel || rel.startsWith('..') || rel.includes(':'))
    throw new Error(`Refusing to write outside ${parent}: ${target}`)
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex')
}

function isValid(path, expected) {
  return existsSync(path) && sha256(path) === expected
}

async function download(url, destination, expectedSha256) {
  ensureInside(resourceRoot, destination)
  if (isValid(destination, expectedSha256))
    return

  let lastError
  for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
    rmSync(destination, { force: true })
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(120_000) })
      if (!response.ok || !response.body)
        throw new Error(`${response.status} ${response.statusText}`)
      await pipeline(response.body, createWriteStream(destination))
      const actual = sha256(destination)
      if (actual !== expectedSha256)
        throw new Error(`expected ${expectedSha256}, got ${actual}`)
      return
    }
    catch (error) {
      lastError = error
      rmSync(destination, { force: true })
      if (attempt < maxAttempts)
        await new Promise(resolve => setTimeout(resolve, 1000 * attempt))
    }
  }
  throw new Error(`Failed to install ${basename(destination)} after ${maxAttempts} attempts`, { cause: lastError })
}

async function extractArchive(archivePath, destination) {
  rmSync(destination, { recursive: true, force: true })
  mkdirSync(destination, { recursive: true })
  const sevenZip = await SevenZip({ print: () => {}, printErr: () => {} })
  sevenZip.FS.mkdir('/source')
  sevenZip.FS.mkdir('/destination')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: dirname(archivePath) }, '/source')
  sevenZip.FS.mount(sevenZip.NODEFS, { root: destination }, '/destination')
  try {
    sevenZip.callMain(['x', `/source/${basename(archivePath)}`, '-o/destination', '-y'])
  }
  catch (error) {
    if (error?.status !== 0)
      throw error
  }
  finally {
    sevenZip.FS.unmount('/source')
    sevenZip.FS.unmount('/destination')
  }
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: rootDir,
    encoding: 'utf8',
    windowsHide: true,
    ...options,
  })
  if (result.status !== 0)
    throw new Error([result.stdout, result.stderr].filter(Boolean).join('\n'))
  return result.stdout.trim()
}

async function installWindowsOnnxModel() {
  const workDir = join(resourceRoot, '.windows-export')
  const archivePath = join(workDir, officialRifeArchive.name)
  const extractDir = join(workDir, 'source')
  const pythonDir = join(workDir, 'python')
  const outputPath = join(modelDir, 'rife-v4.6-flow-mask.onnx')
  const licensePath = join(modelDir, officialRifeLicense.name)
  mkdirSync(workDir, { recursive: true })
  if (!isValid(outputPath, officialRifeArchive.outputSha256)) {
    await download(officialRifeArchive.url, archivePath, officialRifeArchive.sha256)
    await extractArchive(archivePath, extractDir)
    const checkpoint = join(extractDir, 'train_log', 'flownet.pkl')
    if (!isValid(checkpoint, officialRifeArchive.checkpointSha256))
      throw new Error('Official Practical-RIFE v4.6 checkpoint checksum mismatch after extraction')
    mkdirSync(pythonDir, { recursive: true })
    run('python', [
      '-m', 'pip', 'install', '--quiet', '--disable-pip-version-check', '--only-binary=:all:',
      '--target', pythonDir, 'torch==2.14.0', 'onnx==1.20.0',
    ])
    run('python', [
      resolve(rootDir, 'scripts', 'export-rife-v4.6-flow-mask.py'),
      '--checkpoint', checkpoint,
      '--output', outputPath,
    ], { env: { ...process.env, PYTHONPATH: pythonDir } })
    if (!isValid(outputPath, officialRifeArchive.outputSha256))
      throw new Error('Reproducible RIFE v4.6 flow/mask ONNX checksum mismatch')
  }
  await download(officialRifeLicense.url, licensePath, officialRifeLicense.sha256)
  rmSync(workDir, { recursive: true, force: true })
}

function modelManifest() {
  return {
    schemaVersion: 1,
    model: modelVersion,
    family: 'RIFE',
    source: 'nihui/rife-ncnn-vulkan',
    sourceCommit: upstreamCommit,
    license: 'MIT',
    inferenceInputs: {
      in0: 'tone-compressed RGB proxy for the earlier frame',
      in1: 'tone-compressed RGB proxy for the later frame',
      in2: 'full-resolution scalar timestep plane in [0,1]',
    },
    inferenceOutputs: {
      flow: {
        blob: '327',
        channels: ['frame0-dx', 'frame0-dy', 'frame1-dx', 'frame1-dy'],
        units: 'full-resolution pixels',
      },
      mask: {
        blob: '332',
        meaning: 'sigmoid blend weight for frame0; frame1 weight is 1-mask',
      },
      prohibitedOutput: {
        blob: 'out0',
        reason: 'The model RGB output is proxy-domain. SDR and HDR are both synthesized from their original FP16 source frames; SDR is linearized before and encoded after the common warp/composite pass.',
      },
    },
    windowsOnnx: {
      source: 'hzwer/Practical-RIFE official v4.6 checkpoint',
      sourceCommit: officialRifeCommit,
      sourceArchiveSha256: officialRifeArchive.sha256,
      checkpointSha256: officialRifeArchive.checkpointSha256,
      exporter: 'scripts/export-rife-v4.6-flow-mask.py',
      exporterDependencies: { torch: '2.14.0', onnx: '1.20.0', opset: 18 },
      output: 'rife-v4.6-flow-mask.onnx',
      outputSha256: officialRifeArchive.outputSha256,
      outputs: ['flow_pixels', 'blend_mask'],
      prohibitedOutput: 'No RGB composite is exported.',
      license: 'MIT',
      licenseSha256: officialRifeLicense.sha256,
    },
    files: Object.fromEntries(modelFiles.map(file => [file.name, { sha256: file.sha256, source: file.url }])),
  }
}

function mirrorAndroidAssets() {
  const androidModelDir = join(androidAssetRoot, 'models', modelVersion)
  mkdirSync(androidModelDir, { recursive: true })
  for (const file of modelFiles)
    copyFileSync(join(modelDir, file.name), join(androidModelDir, file.name))
  copyFileSync(join(resourceRoot, 'model-manifest.json'), join(androidAssetRoot, 'manifest.json'))
  copyFileSync(join(resourceRoot, 'NOTICE.md'), join(androidAssetRoot, 'NOTICE.md'))
}

async function setup() {
  mkdirSync(modelDir, { recursive: true })
  for (const file of modelFiles)
    await download(file.url, join(modelDir, file.name), file.sha256)

  if (process.argv.includes('--windows'))
    await installWindowsOnnxModel()

  writeFileSync(join(resourceRoot, 'model-manifest.json'), `${JSON.stringify(modelManifest(), null, 2)}\n`)
  if (process.argv.includes('--android'))
    mirrorAndroidAssets()
  const platforms = [process.argv.includes('--android') && 'Android', process.argv.includes('--windows') && 'Windows']
    .filter(Boolean)
  console.log(`installed verified ${modelVersion} flow/mask assets${platforms.length ? ` for ${platforms.join(' + ')}` : ''}`)
}

setup().catch((error) => {
  console.error(error)
  process.exit(1)
})
