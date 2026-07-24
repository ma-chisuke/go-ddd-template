# syntax=docker/dockerfile:1

# DB bring-up 用の使い捨て init コンテナのイメージ。
# 宣言的スキーマ適用に psqldef（sqldef）を、ロール/seed/fixtures 適用に psql を用いる。
# psqldef を Go でビルドし、psql を含む postgres イメージに載せた 2 段構成。
# ビルドコンテキストはリポジトリのルート（deploy/apply.sh を含む）を想定する。

# ---- ビルドステージ: psqldef を導入 ----
FROM golang:1.26-alpine AS build
ENV CGO_ENABLED=0
# psqldef は sqldef リポジトリのサブコマンド。版は tools/versions.env（PSQLDEF_VERSION）を
# 単一情報源とし、ビルド ARG で受け取る（既定値は置かない = 未指定ならビルドを失敗させ、
# Dockerfile 側に第 2 の版情報源を作らない。R-6）。compose からは build.args で渡す。
ARG PSQLDEF_VERSION
RUN go install github.com/sqldef/sqldef/cmd/psqldef@${PSQLDEF_VERSION}

# ---- ランタイムステージ: psql（postgres クライアント）+ psqldef ----
FROM postgres:17-alpine
COPY --from=build /go/bin/psqldef /usr/local/bin/psqldef
COPY deploy/apply.sh /usr/local/bin/apply.sh
RUN chmod +x /usr/local/bin/apply.sh

# 各コンテキストの db/ は実行時にボリュームとしてマウントする（/db/inventory, /db/ordering）。
# schema.sql / sqldef.yml / roles.sql / seed.sql / fixtures.sql を、この使い捨てコンテナが読む。
ENV DB_DIR=/db
ENTRYPOINT ["/usr/local/bin/apply.sh"]
