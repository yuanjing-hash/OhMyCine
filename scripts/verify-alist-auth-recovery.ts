import assert from 'node:assert/strict'
import { AlistDataSource, type AlistFetch } from '../src/services/datasource/alist.ts'
import type { AlistCredentialValue } from '../src/services/datasource/credentialStore.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'

const credentialRef = 'datasource:alist-recover:alist-credential'

await verifyStaleTokenRefreshesAndRetries()
await verifyConcurrentExpiredTokenUsesOneRefresh()
await verifyOfflineFailureDoesNotPoisonFutureBrowse()

console.log(JSON.stringify({
  staleTokenRecovery: 'passed',
  concurrentRefreshGuard: 'passed',
  offlineRetryRecovery: 'passed',
}, null, 2))

async function verifyStaleTokenRefreshesAndRetries(): Promise<void> {
  let storedCredential: AlistCredentialValue = {
    token: 'old-token',
    username: 'alice',
    password: 'stored-password',
  }
  let savedCredential: AlistCredentialValue | null = null
  const authorizations: string[] = []
  let loginCount = 0
  let listCount = 0

  const fetcher: AlistFetch = async <T = unknown>(request: Parameters<AlistFetch>[0], options?: Parameters<AlistFetch>[1]) => {
    const url = String(request)
    if (url.endsWith('/api/fs/list')) {
      listCount += 1
      const headers = options?.headers as Record<string, string> | undefined
      authorizations.push(headers?.Authorization ?? '')
      if (listCount === 1)
        return { code: 401, message: 'token invalid', data: null } as T

      return {
        code: 200,
        data: {
          content: [
            { name: 'Recovered.mkv', path: '/Recovered.mkv', is_dir: false, size: 1024 },
          ],
        },
      } as T
    }

    if (url.endsWith('/api/auth/login')) {
      loginCount += 1
      const body = options?.body as Record<string, unknown> | undefined
      assert.equal(body?.username, storedCredential.username)
      assert.equal(body?.password, storedCredential.password)
      return { code: 200, data: { token: 'new-token' } } as T
    }

    throw new Error(`Unexpected OpenList request: ${url}`)
  }

  const source = new AlistDataSource({
    fetcher,
    readCredential: async ref => ref === credentialRef ? storedCredential : null,
    saveCredential: async (ref, value) => {
      assert.equal(ref, credentialRef)
      savedCredential = value
      storedCredential = value
    },
  })
  await source.init(createConfig('alist-recover'))

  const items = await source.list('/')

  assert.equal(items.length, 1)
  assert.equal(items[0]?.name, 'Recovered.mkv')
  assert.equal(loginCount, 1)
  assert.equal(listCount, 2)
  assert.deepEqual(authorizations, ['old-token', 'new-token'])
  assert.equal(savedCredential?.token, 'new-token')
  assert.equal(savedCredential?.username, 'alice')
  assert.equal(savedCredential?.password, 'stored-password')
  assert.equal(source.isConnected, true)
}

async function verifyConcurrentExpiredTokenUsesOneRefresh(): Promise<void> {
  let storedCredential: AlistCredentialValue = {
    token: 'expired-token',
    username: 'carol',
    password: 'stored-password',
  }
  let loginCount = 0
  let listCount = 0
  let resolveLogin: ((value: unknown) => void) | null = null
  const authorizations: string[] = []

  const fetcher: AlistFetch = async <T = unknown>(request: Parameters<AlistFetch>[0], options?: Parameters<AlistFetch>[1]) => {
    const url = String(request)
    if (url.endsWith('/api/fs/list')) {
      listCount += 1
      const headers = options?.headers as Record<string, string> | undefined
      authorizations.push(headers?.Authorization ?? '')

      if (headers?.Authorization === 'expired-token')
        return { code: 401, message: 'not logged in', data: null } as T

      return {
        code: 200,
        data: {
          content: [
            { name: `Concurrent-${listCount}.mkv`, path: `/Concurrent-${listCount}.mkv`, is_dir: false, size: 512 },
          ],
        },
      } as T
    }

    if (url.endsWith('/api/auth/login')) {
      loginCount += 1
      assert.equal(loginCount, 1)
      const body = options?.body as Record<string, unknown> | undefined
      assert.equal(body?.username, storedCredential.username)
      assert.equal(body?.password, storedCredential.password)
      return await new Promise<T>((resolve) => {
        resolveLogin = resolve as (value: unknown) => void
      })
    }

    throw new Error(`Unexpected OpenList request: ${url}`)
  }

  const source = new AlistDataSource({
    fetcher,
    readCredential: async ref => ref === credentialRef ? storedCredential : null,
    saveCredential: async (ref, value) => {
      assert.equal(ref, credentialRef)
      storedCredential = value
    },
  })
  await source.init(createConfig('alist-concurrent-recover'))

  const first = source.list('/')
  const second = source.list('/')

  await waitFor(() => loginCount === 1)
  assert.equal(resolveLogin != null, true)
  resolveLogin?.({ code: 200, data: { token: 'fresh-token' } })

  const [firstItems, secondItems] = await Promise.all([first, second])

  assert.equal(firstItems.length, 1)
  assert.equal(secondItems.length, 1)
  assert.equal(loginCount, 1)
  assert.equal(listCount, 4)
  assert.deepEqual(authorizations, ['expired-token', 'expired-token', 'fresh-token', 'fresh-token'])
  assert.equal(storedCredential.token, 'fresh-token')
  assert.equal(source.isConnected, true)
}

async function verifyOfflineFailureDoesNotPoisonFutureBrowse(): Promise<void> {
  let listCount = 0
  let loginCount = 0
  const fetcher: AlistFetch = async <T = unknown>(request: Parameters<AlistFetch>[0]) => {
    const url = String(request)
    if (url.endsWith('/api/fs/list')) {
      listCount += 1
      if (listCount === 1)
        throw new Error('Failed to fetch')

      return {
        code: 200,
        data: {
          content: [
            { name: 'AfterStartup.mkv', path: '/AfterStartup.mkv', is_dir: false, size: 2048 },
          ],
        },
      } as T
    }

    if (url.endsWith('/api/auth/login')) {
      loginCount += 1
      return { code: 200, data: { token: 'should-not-be-needed' } } as T
    }

    throw new Error(`Unexpected OpenList request: ${url}`)
  }

  const source = new AlistDataSource({
    fetcher,
    readCredential: async () => ({
      token: 'valid-token',
      username: 'bob',
      password: 'stored-password',
    }),
    saveCredential: async () => {
      throw new Error('Token should not be refreshed for a plain network failure.')
    },
  })
  await source.init(createConfig('alist-offline'))

  await assert.rejects(() => source.list('/'), /Failed to fetch/)
  assert.equal(source.isConnected, false)

  const items = await source.list('/')

  assert.equal(items.length, 1)
  assert.equal(items[0]?.name, 'AfterStartup.mkv')
  assert.equal(listCount, 2)
  assert.equal(loginCount, 0)
  assert.equal(source.isConnected, true)
}

async function waitFor(predicate: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (predicate())
      return
    await new Promise(resolve => setTimeout(resolve, 0))
  }
  throw new Error('Timed out waiting for assertion condition.')
}

function createConfig(id: string): DataSourceConfig {
  return {
    id,
    type: 'alist',
    name: 'OpenList/Alist',
    displayName: 'OpenList/Alist',
    order: 0,
    url: 'http://openlist.local:5244',
    enabled: true,
    extra: {
      credentialRef,
      rootPath: '/',
    },
  }
}
