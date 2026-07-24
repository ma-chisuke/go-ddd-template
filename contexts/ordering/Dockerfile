# syntax=docker/dockerfile:1

# 注文サービスのマルチステージビルド。
# ビルドコンテキストはリポジトリのルート（shared・clients・contexts を含む）を想定する。
# 単一モジュールモード（GOWORK=off）でビルドするため go.work は不要。各モジュールの
# replace ディレクティブがローカルの shared / clients/inventory を解決する。

# ---- ビルドステージ ----
FROM golang:1.25-alpine AS build
WORKDIR /src
# 静的バイナリを作る（distroless で動かすため CGO を無効化）。単一モジュールでビルドする。
ENV CGO_ENABLED=0 GOOS=linux GOWORK=off

# 依存モジュールと注文サービスをコピーする。
COPY shared/ ./shared/
COPY clients/ ./clients/
COPY contexts/ordering/ ./contexts/ordering/

# 注文サービスのバイナリをビルドする（-trimpath と -ldflags でサイズを削る）。
# GOWORK=off の単一モジュールモードでは go.mod のあるモジュールルートから実行する
# （replace の ../../shared・../../clients/inventory はそれぞれ /src/shared・/src/clients/inventory に解決される）。
WORKDIR /src/contexts/ordering
RUN go build -trimpath -ldflags="-s -w" -o /out/ordering ./cmd/ordering

# ---- ランタイムステージ ----
# distroless の static:nonroot は最小構成かつ非 root 実行。シェルもパッケージも持たない。
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ordering /ordering
USER nonroot:nonroot
EXPOSE 8082
ENTRYPOINT ["/ordering"]
