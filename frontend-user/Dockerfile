# syntax=docker/dockerfile:1

# ---------------------------------------------------------------- builder
FROM node:22-alpine AS builder

# CN 网络三件套：apk 换阿里云 + npm 换 npmmirror。
# 不依赖宿主环境变量，保证任何机器上 build 行为一致（KB [Docker][CN网络]）。
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && npm config set registry https://registry.npmmirror.com

WORKDIR /app

# 先装依赖再拷源码，让 node_modules 层可缓存。
# 用 npm ci 而不是 npm install：ci 严格按 lock 文件装，构建可复现。
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# Vite 在**构建期**把 import.meta.env 烘焙进产物，因此这两个值必须
# 通过 build args 传入，运行时再改环境变量是没有任何效果的
# （KB [Docker][Vite]）。docker-compose.yml 的 build.args 负责传值。
ARG VITE_API_BASE=""
ARG VITE_WS_BASE=""
ENV VITE_API_BASE=${VITE_API_BASE} \
    VITE_WS_BASE=${VITE_WS_BASE}

RUN npm run build

# ------------------------------------------------------------------ final
FROM nginx:1.27-alpine

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache tzdata wget \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

ENV TZ=Asia/Shanghai

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=12 \
    CMD wget -qO- http://127.0.0.1/ >/dev/null 2>&1 || exit 1

CMD ["nginx", "-g", "daemon off;"]
