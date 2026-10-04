# 多阶段构建：Node 构建前端 → Go 编译（前端产物 embed 进二进制）→ distroless 运行镜像。
# 前两个阶段在构建机的平台上运行，Go 交叉编译到目标平台，构建多平台镜像时不需要模拟。

# Node 版本满足 frontend/package.json 的 engines；pnpm 由 corepack 按 packageManager 安装
FROM --platform=$BUILDPLATFORM node:24-slim AS frontend
# pnpm 的 store 放进缓存挂载（pnpm 12 只认 pnpm_config_ 前缀的环境变量），依赖变了也不必重新下载全部包
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 pnpm_config_store_dir=/pnpm/store
RUN corepack enable
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/pnpm/store \
    pnpm install --frozen-lockfile
COPY frontend/ ./
# 产物输出到 /src/backend/web/static/dist（见 vite.config.ts）
RUN pnpm build

# Go 版本与 backend/go.mod 对齐
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/backend/web/static/dist ./web/static/dist
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/danfuse ./cmd/server

# 只有静态二进制，带 CA 证书与时区数据，以 nonroot（65532）运行
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=backend /out/danfuse /usr/local/bin/danfuse
USER nonroot:nonroot
EXPOSE 8080
# 不传 -config：只用默认值和 DANFUSE_ 前缀的环境变量
ENTRYPOINT ["/usr/local/bin/danfuse"]
