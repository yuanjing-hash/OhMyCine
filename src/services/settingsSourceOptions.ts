import type { DataSourceConfig, DataSourceType } from '@/services/datasource/types'
import type { SubtitleLanguage } from '@/services/subtitle'

export type LoginDataSourceType = Extract<DataSourceType, 'server' | 'emby' | 'jellyfin'>
export type EditableDataSourceType = LoginDataSourceType
export type EditableDataSourceConfig = DataSourceConfig & { type: EditableDataSourceType }

export interface SourceTypeOption {
  type: EditableDataSourceType
  label: string
  shortLabel: string
  description: string
  defaultName: string
  urlPlaceholder: string
  usernamePlaceholder: string
}

export const SOURCE_TYPE_OPTIONS: SourceTypeOption[] = [
  sourceOption('server', 'OhMyCine Server', 'S', '连接 Server 媒体库', 'OhMyCine Server', 'http://127.0.0.1:3000', 'Server 用户名'),
  sourceOption('emby', 'Emby', 'E', '连接 Emby 媒体服务器', 'Emby', 'http://emby.example.test:8096', 'Emby 登录账号'),
  sourceOption('jellyfin', 'Jellyfin', 'J', '连接 Jellyfin 媒体服务器', 'Jellyfin', 'http://jellyfin.example.test:8096', 'Jellyfin 登录账号'),
]

export const SUBTITLE_LANGUAGE_OPTIONS: Array<{ value: SubtitleLanguage, label: string }> = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
]

export function isLoginDataSourceType(type: DataSourceType): type is LoginDataSourceType {
  return type === 'server' || type === 'emby' || type === 'jellyfin'
}

export function isEditableDataSourceType(type: DataSourceType): type is EditableDataSourceType {
  return isLoginDataSourceType(type)
}

export function isEditableDataSourceConfig(config: DataSourceConfig): config is EditableDataSourceConfig {
  return isEditableDataSourceType(config.type)
}

export function sourceTypeLabel(type: DataSourceType): string {
  return SOURCE_TYPE_OPTIONS.find(option => option.type === type)?.label ?? type
}

export function defaultDisplayName(type: EditableDataSourceType): string {
  return SOURCE_TYPE_OPTIONS.find(option => option.type === type)?.defaultName ?? '数据源'
}

function sourceOption(type: EditableDataSourceType, label: string, shortLabel: string, description: string, defaultName: string, urlPlaceholder: string, usernamePlaceholder: string): SourceTypeOption {
  return { type, label, shortLabel, description, defaultName, urlPlaceholder, usernamePlaceholder }
}
