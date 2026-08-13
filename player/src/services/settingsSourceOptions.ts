import type { DataSourceConfig, DataSourceType } from '@/services/datasource/types'
import type { TmdbAuthType } from '@/services/scraper/tmdb'
import type { SubtitleLanguage } from '@/services/subtitle'

export type LoginDataSourceType = Extract<DataSourceType, 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'webdav' | '123' | 'quark'>
export type EditableDataSourceType = LoginDataSourceType | 'local'
export type EditableDataSourceConfig = DataSourceConfig & { type: EditableDataSourceType }

interface SourceTypeOptionBase {
  label: string
  shortLabel: string
  description: string
  defaultName: string
  urlPlaceholder: string
  usernamePlaceholder: string
}

export type SourceTypeOption
  = | SourceTypeOptionBase & { type: EditableDataSourceType, available: true }
    | SourceTypeOptionBase & { type: '115', available: false }

export const SOURCE_TYPE_OPTIONS: SourceTypeOption[] = [
  sourceOption('emby', 'Emby', 'E', '媒体服务器账号登录', 'Emby', 'http://emby.example.test:8096', 'Emby 登录账号'),
  sourceOption('jellyfin', 'Jellyfin', 'J', 'Jellyfin 媒体服务器账号登录', 'Jellyfin', 'http://jellyfin.example.test:8096', 'Jellyfin 登录账号'),
  sourceOption('alist', 'OpenList/Alist', 'A', 'OpenList/Alist API 账号登录', 'OpenList/Alist', 'http://openlist.example.test:5244', 'OpenList/Alist 登录账号'),
  sourceOption('clouddrive2', 'CloudDrive2', 'C', 'CloudDrive2 原生 gRPC API Token', 'CloudDrive2', 'http://clouddrive2.example.test:19798', ''),
  sourceOption('webdav', 'WebDAV', 'W', '通用 WebDAV 只读数据源', 'WebDAV', 'https://dav.example.test/media', 'WebDAV 用户名'),
  sourceOption('quark', '夸克网盘', 'Q', 'Cookie 登录，只读浏览与播放', '夸克网盘', '', ''),
  sourceOption('123', '123 云盘', '2', '账号或访问令牌登录，只读浏览与播放', '123 云盘', '', '123 云盘手机号或邮箱'),
  sourceOption('local', '本地文件夹', 'L', '只读扫描本机媒体目录', '本地媒体库', '', ''),
  {
    type: '115',
    available: false,
    label: '115 网盘',
    shortLabel: '1',
    description: '即将推出：115 登录、只读浏览与播放',
    defaultName: '115 网盘',
    urlPlaceholder: '',
    usernamePlaceholder: '',
  },
]

export const TMDB_AUTH_TYPE_OPTIONS: Array<{ value: TmdbAuthType, label: string, description: string }> = [
  { value: 'readAccessToken', label: 'API 读访问令牌 / Read Access Token', description: '推荐填写。粘贴 TMDB 设置页生成的 v4 只读访问令牌。' },
  { value: 'apiKey', label: 'API Key', description: '兼容旧版 v3 或短 key；已有 API Key 时可继续使用。' },
]

export const TMDB_LANGUAGE_OPTIONS = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en-US', label: 'English' },
  { value: 'ja-JP', label: '日本語' },
  { value: 'ko-KR', label: '한국어' },
]

export const TMDB_REGION_OPTIONS = [
  { value: 'CN', label: '中国内地' },
  { value: 'TW', label: '中国台湾' },
  { value: 'HK', label: '中国香港' },
  { value: 'US', label: '美国' },
  { value: 'JP', label: '日本' },
  { value: 'KR', label: '韩国' },
]

export const SUBTITLE_LANGUAGE_OPTIONS: Array<{ value: SubtitleLanguage, label: string }> = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
]

export function isLoginDataSourceType(type: DataSourceType): type is LoginDataSourceType {
  return type === 'emby' || type === 'jellyfin' || type === 'alist' || type === 'clouddrive2' || type === 'webdav' || type === '123' || type === 'quark'
}

export function isEditableDataSourceType(type: DataSourceType): type is EditableDataSourceType {
  return type === 'local' || isLoginDataSourceType(type)
}

export function isEditableDataSourceConfig(config: DataSourceConfig): config is EditableDataSourceConfig {
  return isEditableDataSourceType(config.type)
}

export function isRootSelectableRemoteSourceType(type: DataSourceType): type is Extract<LoginDataSourceType, 'alist' | 'clouddrive2' | 'webdav' | '123' | 'quark'> {
  return type === 'alist' || type === 'clouddrive2' || type === 'webdav' || type === '123' || type === 'quark'
}

export function sourceTypeLabel(type: DataSourceType): string {
  return SOURCE_TYPE_OPTIONS.find(option => option.type === type)?.label ?? (type === 'server' ? 'OhMyCine Server' : type === 'jellyfin' ? 'Jellyfin' : type)
}

export function defaultDisplayName(type: EditableDataSourceType): string {
  return SOURCE_TYPE_OPTIONS.find(option => option.type === type)?.defaultName ?? '数据源'
}

function sourceOption(type: EditableDataSourceType, label: string, shortLabel: string, description: string, defaultName: string, urlPlaceholder: string, usernamePlaceholder: string): SourceTypeOption {
  return { type, available: true, label, shortLabel, description, defaultName, urlPlaceholder, usernamePlaceholder }
}
