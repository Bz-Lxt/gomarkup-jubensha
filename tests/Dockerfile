# syntax=docker/dockerfile:1

# 测试运行器。放在容器里跑而不是依赖开发机的 Python 环境，
# 这样「在我机器上能跑」这句话不会成为交付的一部分。
FROM python:3.12-alpine

# CN 网络：pip 换清华源（KB [Docker][CN网络]）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && pip config set global.index-url https://pypi.tuna.tsinghua.edu.cn/simple

ENV TZ=Asia/Shanghai \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /tests

COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# 默认跑完整套件：API 冒烟 + WebSocket 行为
CMD ["sh", "-c", "python api_smoke.py && python ws_smoke.py"]
