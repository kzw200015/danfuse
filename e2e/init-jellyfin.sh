#!/usr/bin/env bash
# 初始化 compose 里的两个 Jellyfin：走完启动向导，建媒体库（关闭元数据下载）并扫描，生成 API key。
# 地址和 API key 写入 .env（JELLYFIN_10_11_URL/_API_KEY、JELLYFIN_12_1_URL/_API_KEY），.env 里的其他行原样保留。
# 可重复执行：向导和 API key 已有时跳过；媒体库的设置每次都重新写入，再完整扫描一遍。
set -euo pipefail

cd "$(dirname "$0")"

ADMIN_USER="admin"
ADMIN_PASSWORD="admin"
API_KEY_APP="danfuse"
# 本脚本作为客户端的标识；登录后再追加 Token
CLIENT='MediaBrowser Client="danfuse-e2e", Device="init-jellyfin.sh", DeviceId="danfuse-e2e-init", Version="1.0"'

# 每行：.env 变量前缀、宿主机上的地址
INSTANCES=(
  "JELLYFIN_10_11 http://localhost:28096"
  "JELLYFIN_12_1 http://localhost:28097"
)

# 每行：媒体库名|类型（空表示不指定类型的混合库）|容器内路径
LIBRARIES=(
  "番剧|tvshows|/media/番剧"
  "电影|movies|/media/电影"
  "其他||/media/其他"
)

for cmd in curl jq; do
  command -v "$cmd" >/dev/null || {
    echo "需要 $cmd" >&2
    exit 1
  }
done
[[ -d media/番剧 ]] || {
  echo "media/ 还没有生成，先运行 ./gen-media.sh" >&2
  exit 1
}

# 只访问本机的 Jellyfin，不走环境变量里的代理；单个请求最多 30 秒，免得连接卡住时一直挂着
CURL=(curl --noproxy '*' -fsS --max-time 30)

# api <方法> <路径> [JSON 请求体]：带上 $AUTH 调用 $URL 上的接口，输出响应体
api() {
  local args=(-X "$1" -H "Authorization: $AUTH" "$URL$2")
  if [[ $# -ge 3 ]]; then
    args+=(-H 'Content-Type: application/json' --data "$3")
  fi
  "${CURL[@]}" "${args[@]}"
}

urlencode() { jq -rn --arg v "$1" '$v | @uri'; }

# set_env <变量> <值>：改写 .env 里的这一行，没有就追加
set_env() {
  local tmp
  tmp=$(mktemp)
  touch .env
  awk -v k="$1" -v v="$2" '
    index($0, k "=") == 1 { print k "=" v; done = 1; next }
    { print }
    END { if (!done) print k "=" v }
  ' .env >"$tmp"
  mv "$tmp" .env
}

# 等 Jellyfin 启动完成。启动过程中 /System/Info/Public 可能先返回不完整的内容，
# 首次启动时进程偶尔还会自己重启一次，所以要求 /health 与版本信息连续 3 次正常
wait_ready() {
  local ok=0
  for _ in $(seq 1 90); do
    if [[ $("${CURL[@]}" "$URL/health" 2>/dev/null) == Healthy ]] &&
      INFO=$("${CURL[@]}" "$URL/System/Info/Public" 2>/dev/null) &&
      jq -e .Version <<<"$INFO" >/dev/null 2>&1; then
      ((++ok >= 3)) && return
    else
      ok=0
    fi
    sleep 2
  done
  echo "$URL 没有在 3 分钟内启动，先 docker compose up -d" >&2
  exit 1
}

run_wizard() {
  AUTH=$CLIENT
  api POST /Startup/Configuration '{"UICulture":"zh-CN","MetadataCountryCode":"CN","PreferredMetadataLanguage":"zh"}'
  api GET /Startup/User >/dev/null # 确保默认用户已建好
  api POST /Startup/User "$(jq -n --arg n "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{Name: $n, Password: $p}')"
  api POST /Startup/RemoteAccess '{"EnableRemoteAccess":true}'
  api POST /Startup/Complete
  echo "  向导已完成，管理员 $ADMIN_USER / $ADMIN_PASSWORD"
}

login() {
  AUTH=$CLIENT
  local token
  token=$(api POST /Users/AuthenticateByName \
    "$(jq -n --arg n "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{Username: $n, Pw: $p}')" | jq -r .AccessToken)
  AUTH="$CLIENT, Token=\"$token\""
}

# 媒体库设置：不读 NFO、不用任何联网的元数据和图片提供者、不从视频截图，
# 标题、年份、季号只由文件夹和文件名决定；只保留文件夹里的本地海报。
# 自动合并同名剧保持 Jellyfin 的默认值（开启），与真实环境一致
library_options() {
  jq -n --arg path "$1" '{
    Enabled: true,
    PathInfos: [{Path: $path}],
    EnableAutomaticSeriesGrouping: true,
    EnablePhotos: false,
    EnableRealtimeMonitor: false,
    EnableInternetProviders: false,
    SaveLocalMetadata: false,
    MetadataSavers: [],
    DisabledLocalMetadataReaders: ["Nfo"],
    EnableEmbeddedTitles: false,
    EnableEmbeddedExtrasTitles: false,
    EnableEmbeddedEpisodeInfos: false,
    EnableChapterImageExtraction: false,
    ExtractChapterImagesDuringLibraryScan: false,
    EnableTrickplayImageExtraction: false,
    ExtractTrickplayImagesDuringLibraryScan: false,
    EnableLUFSScan: false,
    AutomaticRefreshIntervalDays: 0,
    AutomaticallyAddToCollection: false,
    PreferredMetadataLanguage: "zh",
    MetadataCountryCode: "CN",
    TypeOptions: (["Series", "Season", "Episode", "Movie", "Video"] | map({
      Type: .,
      MetadataFetchers: [],
      MetadataFetcherOrder: [],
      ImageFetchers: [],
      ImageFetcherOrder: []
    }))
  }'
}

ensure_libraries() {
  local folders line name type path id options query
  folders=$(api GET /Library/VirtualFolders)
  for line in "${LIBRARIES[@]}"; do
    IFS='|' read -r name type path <<<"$line"
    options=$(library_options "$path")
    id=$(jq -r --arg n "$name" '.[] | select(.Name == $n) | .ItemId' <<<"$folders")
    if [[ -n $id ]]; then
      api POST /Library/VirtualFolders/LibraryOptions \
        "$(jq -n --arg id "$id" --argjson o "$options" '{Id: $id, LibraryOptions: $o}')"
      echo "  媒体库 $name 已存在，重新写入设置"
    else
      query="name=$(urlencode "$name")&refreshLibrary=false"
      [[ -n $type ]] && query+="&collectionType=$type"
      api POST "/Library/VirtualFolders?$query" "$(jq -n --argjson o "$options" '{LibraryOptions: $o}')"
      echo "  新建媒体库 $name（${type:-混合}）→ $path"
    fi
  done
}

# 完整扫描一遍媒体库，等扫描任务结束
scan() {
  local task_id task before status
  task_id=$(api GET '/ScheduledTasks?isHidden=false' | jq -r '.[] | select(.Key == "RefreshLibrary") | .Id')
  wait_task_idle "$task_id"
  before=$(api GET "/ScheduledTasks/$task_id" | jq -r '.LastExecutionResult.EndTimeUtc // ""')
  api POST "/ScheduledTasks/Running/$task_id"
  for _ in $(seq 1 150); do
    sleep 2
    task=$(api GET "/ScheduledTasks/$task_id")
    if [[ $(jq -r .State <<<"$task") == Idle && $(jq -r '.LastExecutionResult.EndTimeUtc // ""' <<<"$task") != "$before" ]]; then
      status=$(jq -r .LastExecutionResult.Status <<<"$task")
      if [[ $status != Completed ]]; then
        echo "扫描没有成功：$status" >&2
        exit 1
      fi
      echo "  扫描结束"
      return
    fi
  done
  echo "扫描没有在 5 分钟内结束" >&2
  exit 1
}

wait_task_idle() {
  for _ in $(seq 1 150); do
    [[ $(api GET "/ScheduledTasks/$1" | jq -r .State) == Idle ]] && return
    sleep 2
  done
  echo "已有的扫描没有在 5 分钟内结束" >&2
  exit 1
}

# 输出名为 $API_KEY_APP 的 API key，没有就输出空
find_api_key() {
  api GET /Auth/Keys | jq -r --arg app "$API_KEY_APP" '[.Items[] | select(.AppName == $app)][0].AccessToken // empty'
}

ensure_api_key() {
  API_KEY=$(find_api_key)
  if [[ -z $API_KEY ]]; then
    api POST "/Auth/Keys?app=$API_KEY_APP"
    API_KEY=$(find_api_key)
    echo "  新建 API key（$API_KEY_APP）"
  fi
  if [[ -z $API_KEY ]]; then
    echo "没有取到 API key（$API_KEY_APP）" >&2
    exit 1
  fi
}

# 用 API key 按 danfuse 的鉴权方式列出各媒体库的条目数，顺便验证 key 可用
summary() {
  AUTH="MediaBrowser Token=\"$API_KEY\""
  local folders name id type counts
  folders=$(api GET /Library/VirtualFolders)
  while IFS=$'\t' read -r name id type; do
    counts=$(api GET "/Items?ParentId=$id&Recursive=true&IsMissing=false&IncludeItemTypes=Series,Season,Episode,Movie,Video" |
      jq -r '[.Items | group_by(.Type)[] | "\(.[0].Type) \(length)"] | join("，")')
    echo "  $name（${type:-混合}）：$counts"
  done < <(jq -r '.[] | [.Name, .ItemId, .CollectionType // ""] | @tsv' <<<"$folders")
}

for instance in "${INSTANCES[@]}"; do
  read -r PREFIX URL <<<"$instance"
  echo "== $URL"
  wait_ready
  echo "  Jellyfin $(jq -r .Version <<<"$INFO")"
  [[ $(jq -r .StartupWizardCompleted <<<"$INFO") == true ]] || run_wizard
  login
  ensure_libraries
  scan
  ensure_api_key
  set_env "${PREFIX}_URL" "$URL"
  set_env "${PREFIX}_API_KEY" "$API_KEY"
  summary
done
echo "API key 已写入 $(pwd)/.env"
