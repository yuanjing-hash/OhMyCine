import type { MediaActionGroup, MediaActionId } from './types'

export interface MediaActionDefinition {
  readonly id: MediaActionId
  readonly label: string
  readonly description?: string
  readonly group: MediaActionGroup
  readonly order: number
}

const DEFINITIONS: readonly MediaActionDefinition[] = [
  action('play', '播放', '开始或继续播放', 'primary', 10),
  action('viewDetails', '查看详情', undefined, 'primary', 20),
  action('openLibrary', '进入媒体库', undefined, 'primary', 30),
  action('markPlayed', '标记为已播放', undefined, 'state', 100),
  action('markUnplayed', '标记为未播放', undefined, 'state', 110),
  action('removeFromContinueWatching', '移出继续观看', undefined, 'state', 120),
  action('favorite', '收藏', undefined, 'organize', 200),
  action('unfavorite', '取消收藏', undefined, 'organize', 210),
  action('addToPlaylist', '添加到播放列表', undefined, 'organize', 220),
  action('addToCollection', '添加到合集', undefined, 'organize', 230),
  action('download', '下载', undefined, 'download', 300),
  action('downloadTo', '下载到…', undefined, 'download', 310),
  action('editMetadata', '编辑元数据', undefined, 'manage', 400),
  action('editArtwork', '编辑图像', undefined, 'manage', 410),
  action('editSubtitles', '编辑字幕', undefined, 'manage', 420),
  action('identify', '识别 / 刮削', undefined, 'manage', 430),
  action('refreshMetadata', '刷新元数据', undefined, 'manage', 440),
  action('rescanLibrary', '重新扫描媒体库', undefined, 'manage', 450),
  action('deleteMedia', '删除', undefined, 'danger', 900),
]

const DEFINITION_BY_ID = new Map(DEFINITIONS.map(definition => [definition.id, definition]))

export function getMediaActionDefinition(id: MediaActionId): MediaActionDefinition {
  const definition = DEFINITION_BY_ID.get(id)
  if (!definition)
    throw new Error(`Unknown media action: ${id}`)
  return definition
}

function action(id: MediaActionId, label: string, description: string | undefined, group: MediaActionGroup, order: number): MediaActionDefinition {
  return { id, label, description, group, order }
}
