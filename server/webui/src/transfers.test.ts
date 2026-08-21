import { describe, expect, it } from 'vitest'
import { canDeleteTransferRecord, formatTransferProgress, shouldRefreshTransferEvent, transferStatusClass, transferStatusLabel, type TransferSummary } from '@/transfers'

const summary = { phase: 'planning', job_status: 'running', processed_files: 0, total_files: 2 } as TransferSummary

describe('media organization presentation', () => {
  it('uses exact transfer and waiting stages', () => {
    expect(transferStatusLabel(summary)).toBe('规划目录')
    expect(transferStatusLabel({ ...summary, job_status: 'waiting_user_action' })).toBe('等待冲突处理')
    expect(transferStatusLabel({ ...summary, phase: 'failed', job_status: 'failed' })).toBe('整理失败')
    expect(transferStatusLabel({ ...summary, phase: 'failed', job_status: 'queued' })).toBe('等待重试')
    expect(transferStatusLabel({ ...summary, phase: 'transferring', job_status: 'cancelled' })).toBe('已取消')
  })

  it('does not invent progress before a plan exists', () => {
    expect(formatTransferProgress({ ...summary, total_files: 0 })).toBe('尚未生成计划')
    expect(formatTransferProgress({ ...summary, processed_files: 1 })).toBe('1 / 2')
  })

  it('exposes non-color status classes', () => {
    expect(transferStatusClass({ ...summary, phase: 'completed', job_status: 'completed' })).toContain('ready')
    expect(transferStatusClass({ ...summary, phase: 'failed', job_status: 'failed' })).toContain('error')
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
})
