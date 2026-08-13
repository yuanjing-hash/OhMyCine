# Player v1 Classification Contract Evidence

Authoritative implementation inspected: `player/src/services/scraper/classificationRules.ts`.

## Schema and Validation Evidence

- `ScrapeClassificationRules.version` is exactly `1`.
- There are `movie` and `tv` groups, ordered categories and one fallback name per group.
- Conditions are include/exclude sets for genre IDs, original language, movie production country or TV origin country, plus an optional inclusive release-year range.
- Player allowlists include the TMDB movie/TV genre IDs declared in that file, languages `zh,cn,en,ja,ko,fr,de,es,it,ru,th,hi`, and countries `CN,TW,HK,JP,KR,US,GB,FR,DE,ES,IT,NL,PT,RU,TH,IN,SG`.
- Player accepts years 1888–2200, compares string condition values case-insensitively, gives excludes priority, treats includes as OR, combines dimensions with AND, and returns the group fallback when no category matches.

## Exact Default Rules

- Movie: 动画电影 → 华语电影 → 外语电影; fallback 未分类.
- TV: 国漫 → 日番 → 动漫 → 纪录片 → 儿童 → 综艺 → 国产剧 → 欧美剧 → 日韩剧; fallback 未分类.
- Exact conditions and codes are recorded in the task PRD and must be asserted by Server fixture tests.

## Server Divergence That Is Intentional

Player loads user-local settings defensively and sanitizes malformed data back toward defaults. Server API writes instead reject unknown fields, invalid allowlist values and contradictory conditions with stable validation errors, because silently changing an administrator's submitted shared rule would be unsafe.

Server implements this contract in Go and must not import, execute or persist Player TypeScript/local settings.
