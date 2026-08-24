# Playwright 官方镜像已内置浏览器与系统依赖，同时提供 amd64/arm64 两个架构，
# 满足跨平台要求。版本号必须与 package.json 里的 @playwright/test 严格一致，
# 否则会出现「浏览器版本与客户端不匹配」的启动失败。
FROM mcr.microsoft.com/playwright:v1.62.1-noble

ENV TZ=Asia/Shanghai \
    CI=1 \
    npm_config_registry=https://registry.npmmirror.com

WORKDIR /e2e

COPY package.json ./
RUN npm install --no-audit --no-fund

COPY playwright.config.ts ./
COPY *.spec.ts ./

CMD ["npx", "playwright", "test"]
