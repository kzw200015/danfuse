#!/usr/bin/env bash
# 生成端到端环境的测试媒体库：几十秒的黑屏视频，按 Jellyfin 的命名规则排好。
# 剧名、年份都是虚构的，标题、年份、季号只由文件夹和文件名决定。
# 可重复执行：先清空 media/ 里的内容再重建（保留目录本身，运行中的 Jellyfin 挂载不受影响）。
set -euo pipefail

cd "$(dirname "$0")"
MEDIA=media

command -v ffmpeg >/dev/null || {
  echo "需要 ffmpeg（macOS: brew install ffmpeg）" >&2
  exit 1
}

mkdir -p "$MEDIA"
find "$MEDIA" -mindepth 1 -delete

# video <路径> <秒数> [分辨率]：黑屏 H.264 + 静音 AAC 的 mkv
video() {
  local path="$MEDIA/$1" seconds=$2 size=${3:-640x360}
  mkdir -p "$(dirname "$path")"
  ffmpeg -nostdin -loglevel error -y \
    -f lavfi -i "color=c=black:s=$size:r=24" \
    -f lavfi -i "anullsrc=r=48000:cl=stereo" \
    -t "$seconds" -c:v libx264 -preset ultrafast -tune stillimage -pix_fmt yuv420p \
    -c:a aac -b:a 32k "$path"
  echo "$path (${seconds}s, $size)"
}

# poster <路径> <测试图源>：600x900 的海报，扩展名决定格式
poster() {
  local path="$MEDIA/$1"
  mkdir -p "$(dirname "$path")"
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "$2=s=600x900" -frames:v 1 "$path"
  echo "$path"
}

# 番剧（tvshows）
# 星海旅人 (2019)：特别篇、多集文件、同一集两个版本、第 2 季、自带 JPEG 海报
poster "番剧/星海旅人 (2019)/poster.jpg" testsrc2
video "番剧/星海旅人 (2019)/Season 00/星海旅人 S00E01.mkv" 20
video "番剧/星海旅人 (2019)/Season 01/星海旅人 S01E01-E02.mkv" 50
video "番剧/星海旅人 (2019)/Season 01/星海旅人 S01E03.mkv" 30
video "番剧/星海旅人 (2019)/Season 01/星海旅人 S01E04 - 1080p.mkv" 34 1920x1080
video "番剧/星海旅人 (2019)/Season 01/星海旅人 S01E04 - 720p.mkv" 33 1280x720
video "番剧/星海旅人 (2019)/Season 02/星海旅人 S02E01.mkv" 30
video "番剧/星海旅人 (2019)/Season 02/星海旅人 S02E02.mkv" 30

# 星海旅人 (2023)：与上面同名、年份不同，没有海报
video "番剧/星海旅人 (2023)/Season 01/星海旅人 S01E01.mkv" 30
video "番剧/星海旅人 (2023)/Season 01/星海旅人 S01E02.mkv" 30

# 雾港谜案 (2021)：中文季文件夹；第1季的文件名不带季号，第2季的带季号；自带 PNG 海报
poster "番剧/雾港谜案 (2021)/poster.png" smptebars
video "番剧/雾港谜案 (2021)/第1季/雾港谜案 - 01.mkv" 30
video "番剧/雾港谜案 (2021)/第1季/雾港谜案 - 02.mkv" 30
video "番剧/雾港谜案 (2021)/第2季/雾港谜案 S02E01.mkv" 30
video "番剧/雾港谜案 (2021)/第2季/雾港谜案 S02E02.mkv" 30

# 青石巷日常 (2022)：之后不绑定的季
video "番剧/青石巷日常 (2022)/Season 01/青石巷日常 S01E01.mkv" 30
video "番剧/青石巷日常 (2022)/Season 01/青石巷日常 S01E02.mkv" 30

# 电影（movies）
poster "电影/长夜灯塔 (2020)/poster.jpg" testsrc
video "电影/长夜灯塔 (2020)/长夜灯塔 (2020).mkv" 45

# 其他（不指定类型的混合库）：同步时应被跳过
video "其他/片段.mkv" 10
