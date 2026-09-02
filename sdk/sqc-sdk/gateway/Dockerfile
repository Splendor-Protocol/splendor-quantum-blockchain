# Build context: sdk/sqc-sdk (run: npm run build && cd gateway && npm ci)
#
# Node 24 for OpenSSL 3.5, which is where X25519MLKEM768 comes from. Node 22
# only picked it up in 22.20 (backport); the floating node:22 tag spent most of
# its life on OpenSSL 3.0, where the gateway offered the hybrid group, OpenSSL
# silently dropped it as unrecognised, and every connection was classical. The
# gateway now refuses to start rather than serve that, so this base image is
# what makes TLS work at all.
FROM node:24-bookworm-slim AS build

WORKDIR /app
COPY package.json package-lock.json ./
COPY tsconfig.json ./
COPY src ./src
COPY config ./config
COPY scripts ./scripts
COPY wasm/dist ./wasm/dist
RUN npm ci && npm run build:ts

WORKDIR /app/gateway
COPY gateway/package.json gateway/package-lock.json ./
COPY gateway/server.ts ./
RUN npm ci

FROM node:24-bookworm-slim AS runtime
RUN useradd -r -u 10001 sqc && mkdir -p /app && chown -R sqc:sqc /app
WORKDIR /app

COPY --from=build /app/dist ./dist
COPY --from=build /app/wasm/dist ./wasm/dist
COPY --from=build /app/config ./config
COPY --from=build /app/package.json ./
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/gateway/package.json ./gateway/
COPY --from=build /app/gateway/node_modules ./gateway/node_modules
COPY --from=build /app/gateway/server.ts ./gateway/

USER sqc
WORKDIR /app/gateway
ENV MODE=proxy
ENV PORT=3090
ENV PUBLIC_HEALTH=0
EXPOSE 3090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||3090)+'/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"
CMD ["node", "--import", "tsx", "server.ts"]
