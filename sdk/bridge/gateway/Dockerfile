# Build context: sdk/bridge (prebuild: npm run build:ts && wasm/build.ps1)
FROM node:22-bookworm-slim

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY wasm/dist ./wasm/dist
COPY node_modules ./node_modules

COPY gateway/package.json ./gateway/
COPY gateway/server.ts ./gateway/
COPY gateway/node_modules ./gateway/node_modules

WORKDIR /app/gateway

ENV SQC_RPC=http://host.docker.internal:8080
ENV MODE=proxy
ENV PORT=3090

EXPOSE 3090
CMD ["npx", "tsx", "server.ts"]
