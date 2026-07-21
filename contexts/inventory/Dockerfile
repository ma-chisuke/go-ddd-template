# syntax=docker/dockerfile:1

# 在庫サービスのマルチステージビルド。
# ビルドコンテキストはリポジトリのルート（shared・contexts を含む）を想定する。
# 単一モジュールモード（GOWORK=off）でビルドするため go.work は不要。在庫モジュールの
# replace ディレクティブがローカルの shared を解決する（コンテキストを単独で切り出せる）。

# ---- ビルドステージ ----
FROM golang:1.25-alpine AS build
WORKDIR /src
# 静的バイナリを作る（distroless で動かすため CGO を無効化）。単一モジュールでビルドする。
ENV CGO_ENABLED=0 GOOS=linux GOWORK=off

# 依存モジュールと在庫サービスをコピーする。
COPY shared/ ./shared/
COPY contexts/inventory/ ./contexts/inventory/

# 在庫サービスのバイナリをビルドする。-trimpath と -ldflags でサイズを削る。
RUN go build -trimpath -ldflags="-s -w" -o /out/inventory ./contexts/inventory/cmd/inventory

# ---- ランタイムステージ ----
# distroless の static:nonroot は最小構成かつ非 root 実行。シェルもパッケージも持たない。
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/inventory /inventory
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/inventory"]
