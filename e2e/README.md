# 端到端环境

用一个虚构的测试媒体库同时起 Jellyfin 10.11 和 12.1，再加上用本地源码构建的 danfuse，用来验收 danfuse 的同步、抓取 Jellyfin 的测试样本、手工试用弹幕插件。

| 服务 | 镜像 | 宿主机地址 |
|---|---|---|
| `jellyfin-10-11` | `jellyfin/jellyfin:10.11.11` | http://localhost:28096 |
| `jellyfin-12-1` | `jellyfin/jellyfin:12.1.20260915-010956` | http://localhost:28097 |
| `postgres` | `postgres:18` | `localhost:25432`，用户、密码、库名都是 `danfuse` |
| `danfuse` | 用仓库根目录的 `Dockerfile` 从本地源码构建 | http://localhost:28080 |

- 10.11 固定到小版本 `10.11.11`。12.x 的版本号只到 `主版本.次版本`，`12.1` 这个标签以后可能指向新的构建，所以固定到带构建时间的 `12.1.20260915-010956`（即 12.1.0，2026-10 时与 `12.1` 是同一个镜像）。
- 两个 Jellyfin 只读挂载同一个 `media/`。Jellyfin 的配置、缓存和 PostgreSQL 的数据都在 compose 的命名卷里（`danfuse-e2e_*`）。
- 端口只绑定在 `127.0.0.1`。要让局域网里的其他设备访问，改 `compose.yaml` 里的 `ports`。

## 启动与初始化

需要 Docker（含 compose）、ffmpeg、curl、jq。

```sh
cd e2e
./gen-media.sh                                           # 生成测试媒体库到 media/
docker compose up -d jellyfin-10-11 jellyfin-12-1 postgres
./init-jellyfin.sh                                       # 初始化两个 Jellyfin
docker compose up -d --build danfuse                     # 构建并启动 danfuse
```

danfuse 要用初始化脚本写进 `.env` 的 API key，所以放在最后启动。

`init-jellyfin.sh` 对两个实例依次：

1. 调用 Startup API 走完向导：界面语言 `zh-CN`，管理员 `admin` / `admin`。
2. 建三个媒体库：`番剧`（tvshows）、`电影`（movies）、`其他`（不指定类型的混合库）。媒体库关闭元数据下载：不用任何联网的元数据和图片提供者，不从视频截图，不读内嵌标题。标题、年份、季号都只由文件夹和文件名决定，每次结果一样；海报只来自文件夹里自带的图片。NFO 照常读取（Jellyfin 关不掉），测试媒体库用它给要同步的剧和电影带上外部 ID，见下文。
3. 完整扫描一遍媒体库，等扫描结束。
4. 生成名为 `danfuse` 的 API key，把地址和 key 写进 `e2e/.env`：

   ```sh
   JELLYFIN_10_11_URL=http://localhost:28096
   JELLYFIN_10_11_API_KEY=...
   JELLYFIN_12_1_URL=http://localhost:28097
   JELLYFIN_12_1_API_KEY=...
   ```

   只改写这四行，`.env` 里的其他行原样保留。`.env` 和 `media/` 都被 git 忽略。

脚本可以重复执行：向导和 API key 已有时跳过，媒体库的设置每次都重新写入，再完整扫描一遍。中途失败时直接重新运行即可。

API key 也可以手工查看或新建：用管理员登录 Jellyfin，进入"控制台 → API 密钥"。

### compose 里的 danfuse

容器里通过服务名访问 Jellyfin，默认同步 12.1（`http://jellyfin-12-1:8096`，API key 取 `.env` 里的 `JELLYFIN_12_1_API_KEY`），媒体库为 `番剧,电影,其他`，不开定时同步。改为同步 10.11 时，在 `.env` 末尾加上（compose 读 `.env` 时会展开前面定义的变量，重建环境、key 变了也不用改）：

```sh
JELLYFIN_URL=http://jellyfin-10-11:8096
JELLYFIN_API_KEY=${JELLYFIN_10_11_API_KEY}
```

再执行 `docker compose up -d danfuse`；删掉这两行再执行一次就切回 12.1。两个版本共用同一个数据库，剧名又不同（见下文），切换后目录里会同时有两边的剧。要从空库开始：

```sh
docker compose stop danfuse
docker compose exec postgres dropdb -U danfuse danfuse
docker compose exec postgres createdb -U danfuse danfuse
docker compose up -d danfuse     # 启动时重新执行迁移
```

B 站的 SESSDATA 写在 `.env` 的 `BILIBILI_SESSDATA=...`。改了源码之后用 `docker compose up -d --build danfuse` 重新构建。

### 连接本地运行的 danfuse

在宿主机上运行后端时，可以直接用这里的 PostgreSQL 和 Jellyfin，不需要配置文件。例如同步 12.1，在 `backend/` 目录下：

```sh
set -a; source ../e2e/.env; set +a
export DANFUSE_DATABASE_DSN="postgres://danfuse:danfuse@localhost:25432/danfuse?sslmode=disable"
export DANFUSE_CATALOG_SOURCE_KIND=jellyfin
export DANFUSE_CATALOG_SOURCE_JELLYFIN_URL=$JELLYFIN_12_1_URL
export DANFUSE_CATALOG_SOURCE_JELLYFIN_API_KEY=$JELLYFIN_12_1_API_KEY
export DANFUSE_CATALOG_SOURCE_JELLYFIN_LIBRARIES=番剧,电影,其他
go run ./cmd/server
```

`其他` 是混合库，同步时应被跳过并记一条警告。

本地运行的 danfuse 和 compose 里的 danfuse 用的是同一个库，只留一个在跑：`docker compose stop danfuse`。

## 停止与重建

```sh
docker compose stop      # 停止，数据保留；docker compose start 再启动
docker compose down -v   # 删除容器和全部数据（包括 API key）

# 从头重建
docker compose down -v && ./gen-media.sh &&
  docker compose up -d jellyfin-10-11 jellyfin-12-1 postgres && ./init-jellyfin.sh &&
  docker compose up -d --build danfuse
```

重建后 Jellyfin 的条目 Id 不变，API key、ServerId 和图片的 tag 会变。

## 测试媒体库

视频都是几十秒的黑屏，时长各不相同，方便核对时长的换算。剧名、年份都是虚构的。

danfuse 只同步刮削过的剧和电影。除了特别注明没刮削的，每部剧的文件夹里都有一个 `tvshow.nfo`，电影旁边有一个与视频同名的 `.nfo`，里面只写了一个虚构的 TMDB ID（`<uniqueid type="tmdb">`），让条目带上 ProviderIds，标题和年份仍由文件夹名决定。

| 路径（`media/` 下） | 覆盖的情况 |
|---|---|
| `番剧/星海旅人 (2019)/poster.jpg` | 文件夹自带的 JPEG 海报 |
| `番剧/星海旅人 (2019)/Season 00/星海旅人 S00E01.mkv` | 特别篇（第 0 季），20 秒 |
| `番剧/星海旅人 (2019)/Season 01/星海旅人 S01E01-E02.mkv` | 多集文件，50 秒 |
| `番剧/星海旅人 (2019)/Season 01/星海旅人 S01E03.mkv` | 普通的集，30 秒 |
| `番剧/星海旅人 (2019)/Season 01/星海旅人 S01E04 - 1080p.mkv`、`… - 720p.mkv` | 同一集的两个版本，34 秒和 33 秒 |
| `番剧/星海旅人 (2019)/Season 02/` | 第 2 季，两集 |
| `番剧/星海旅人 (2023)/Season 01/` | 与上面同名、年份不同的剧，没有海报 |
| `番剧/雾港谜案 (2021)/poster.png` | 文件夹自带的 PNG 海报 |
| `番剧/雾港谜案 (2021)/第1季/雾港谜案 - 01.mkv`、`- 02.mkv` | 中文季文件夹，文件名不带季号 |
| `番剧/雾港谜案 (2021)/第2季/雾港谜案 S02E01.mkv`、`S02E02.mkv` | 中文季文件夹，文件名带季号 |
| `番剧/青石巷日常 (2022)/Season 01/` | 验收时不绑定的季 |
| `番剧/山间来信 (2024)/Season 01/` | 没有 NFO，没刮削，同步时跳过 |
| `电影/长夜灯塔 (2020)/` | 电影，45 秒，自带 JPEG 海报 |
| `电影/旧港夜航 (2018)/` | 没有 NFO 的电影，没刮削，同步时跳过 |
| `其他/片段.mkv` | 混合库里的视频 |

两个版本对这个媒体库的识别结果不同，验收时要注意：

- 剧名：10.11 保留文件夹名里的年份（`星海旅人 (2019)`），12.1 去掉（`星海旅人`）。电影名两个版本都保留年份（`长夜灯塔 (2020)`）。年份都能正确识别。
- 同一集的两个版本：10.11 是两个独立的集，12.1 合并成一个。
- 同名剧：12.1 按剧名自动合并剧（两部的外部 ID 不同也照样合并），两部"星海旅人"在 Jellyfin 界面里显示为一部；10.11 的剧名带年份，不会合并。
- 季名：`Season 01` 这类文件夹的季名是 Jellyfin 按界面语言生成的"第 1 季"，`Season 00` 是"Specials"。`第1季` 这类文件夹认不出季号，季名就是文件夹名；里面文件名不带季号的集也没有季号。文件名带季号、但季文件夹解析不出季号时（`第2季` 里的 `S02E01`），Jellyfin 会补建一个季：10.11 叫"第 2 季"，12.1 叫"Season 2"。

## 安装 jellyfin-danmaku 插件

[jellyfin-danmaku](https://github.com/Izumiko/jellyfin-danmaku) 不是 Jellyfin 的服务端插件，而是要注入到 Jellyfin Web 页面里的脚本，初始化脚本不处理，需要人工安装。任选一种：

**浏览器用户脚本**（不改容器）

1. 安装 [Tampermonkey](https://www.tampermonkey.net/)，按它的说明开启运行用户脚本所需的开发者模式。
2. 添加脚本 https://cdn.jsdelivr.net/gh/Izumiko/jellyfin-danmaku@gh-pages/ede.user.js 。
3. 打开 http://localhost:28096 或 http://localhost:28097 。

**改容器里的 Jellyfin Web**

```sh
for s in jellyfin-10-11 jellyfin-12-1; do
  docker compose exec "$s" sed -i 's#</body>#<script src="https://cdn.jsdelivr.net/gh/Izumiko/jellyfin-danmaku@gh-pages/ede.user.js" defer></script></body>#' /jellyfin/jellyfin-web/index.html
done
```

然后在浏览器里强制刷新 Jellyfin 页面。只执行一次，重复执行会注入两遍；容器重建（`docker compose down`、换镜像）后要重新执行。

**指向 danfuse**

播放任意一集，在"设置 → 弹幕设置"里把自定义 API 填成 danfuse 的插件地址：compose 里的 danfuse 是 `http://localhost:28080/dandanplay`，本地运行的是 `http://localhost:8080/dandanplay`（配置了 token 时在后面加 `/<token>`，末尾不带 `/`）。插件会在后面拼 `/api/v2`。

插件把匹配结果按 Jellyfin 的季存在浏览器的 localStorage 里，永不过期。重建 danfuse 的数据库后，要清掉 Jellyfin 页面的站点数据再试；两个 Jellyfin 端口不同，各自独立。
