import { describe, expect, it } from 'vitest'
import { canDeleteTransferRecord, formatTransferProgress, shouldRefreshTransferEvent, transferDeletionLabels, transferIdentityLabel, transferPhaseDescription, transferRouteLabel, transferStatusClass, transferStatusLabel, type TransferSummary } from '@/transfers'

const summary = { phase: 'planning', job_status: 'running', processed_files: 0, total_files: 2 } as TransferSummary

describe('media organization presentation', () => {
  it('uses exact transfer and waiting stages', () => {
    expect(transferStatusLabel(summary)).toBe('规划目录')
    expect(transferStatusLabel({ ...summary, job_status: 'waiting_user_action' })).toBe('等待冲突处理')
    expect(transferStatusLabel({ ...summary, phase: 'failed', job_status: 'failed' })).toBe('整理失败')
    expect(transferStatusLabel({ ...summary, phase: 'failed', job_status: 'queued' })).toBe('等待重试')
    expect(transferStatusLabel({ ...summary, phase: 'transferring', job_status: 'cancelled' })).toBe('已取消')
    expect(transferStatusLabel({ ...summary, phase: 'checking_directories' })).toBe('检查目标目录')
    expect(transferStatusLabel({ ...summary, phase: 'materializing' })).toBe('拉取到 Server 暂存')
    expect(transferStatusLabel({ ...summary, phase: 'verifying' })).toBe('校验完整性')
    expect(transferStatusLabel({ ...summary, phase: 'uploading' })).toBe('上传到目标网盘')
    expect(transferStatusLabel({ ...summary, phase: 'reconciling' })).toBe('媒体库对账')
    expect(transferPhaseDescription({ ...summary, phase: 'risk_backoff', retry_at: null })).toContain('等待安全重试')
  })

  it('does not invent progress before a plan exists', () => {
    expect(formatTransferProgress({ ...summary, total_files: 0 })).toBe('尚未生成计划')
    expect(formatTransferProgress({ ...summary, processed_files: 1 })).toBe('1 / 2')
  })

  it('exposes non-color status classes', () => {
    expect(transferStatusClass({ ...summary, phase: 'completed', job_status: 'completed' })).toContain('ready')
    expect(transferStatusClass({ ...summary, phase: 'failed', job_status: 'failed' })).toContain('error')
  })

  it('distinguishes locked, AI and provisional identities', () => {
    expect(transferIdentityLabel({ ...summary, identity_locked: true, identity_source: 'manual', identity_status: 'verified', identity_revision: 3 })).toBe('人工锁定 · r3')
    expect(transferIdentityLabel({ ...summary, identity_locked: false, identity_source: 'ai', identity_status: 'provisional', identity_revision: 2 })).toBe('AI 辅助 · r2')
    expect(transferIdentityLabel({ ...summary, identity_locked: false, identity_source: 'automatic', identity_status: 'provisional', identity_revision: 1 })).toBe('自动暂定 · r1')
  })

  it('renders only the frozen server route instead of inferring from provider types', () => {
    expect(transferRouteLabel('same_source_local')).toBe('同源本地')
    expect(transferRouteLabel('same_source_provider')).toBe('同源云端')
    expect(transferRouteLabel('cross_source')).toBe('跨数据源暂存')
    expect(transferRouteLabel('')).toBe('旧任务路线未知')
  })

  it('refreshes only transfer or visible job events', () => {
    const visible = new Set(['visible-job'])
    expect(shouldRefreshTransferEvent(JSON.stringify({ data: { job_id: 'other', job_type: 'transfer' } }), visible)).toBe(true)
    expect(shouldRefreshTransferEvent(JSON.stringify({ data: { job_id: 'visible-job', job_type: 'download' } }), visible)).toBe(true)
    expect(shouldRefreshTransferEvent(JSON.stringify({ data: { job_id: 'other', job_type: 'download' } }), visible)).toBe(false)
    expect(shouldRefreshTransferEvent('invalid', visible)).toBe(false)
  })

  it('only offers record deletion for terminal tasks', () => {
    for (const job_status of ['failed', 'cancelled', 'completed'] as const) {
      expect(canDeleteTransferRecord({ ...summary, job_status })).toBe(true)
    }
    for (const job_status of ['queued', 'running', 'waiting_user_action', 'retry_wait', 'paused'] as const) {
      expect(canDeleteTransferRecord({ ...summary, job_status })).toBe(false)
    }
  })

  it('exposes four explicit deletion scopes without collapsing destructive choices', () => {
    expect(Object.keys(transferDeletionLabels)).toEqual(['record_only', 'record_and_source', 'record_and_library', 'record_source_and_library'])
    expect(transferDeletionLabels.record_only).toBe('仅删除转移记录')
    expect(transferDeletionLabels.record_source_and_library).toContain('源文件和媒体库文件')
  })
})
