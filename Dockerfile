# Docker Control 完整镜像
# = 官方 diancup 后端（闭源二进制，原样保留）+ 我们改造完成的纯 Docker 管理前端（直接打入镜像，无需挂载覆盖）
# 基础镜像的多架构支持以 yjnas/diancup 官方为准
FROM yjnas/diancup:latest

# 覆盖前端：改造后的完整 static（tradis 风格皮肤 + 横条仪表板 + 双栏端口页 + 品牌化）
COPY static/ /app/static/

# 继承官方镜像的 ENTRYPOINT/CMD/VOLUME，仅替换前端资产
EXPOSE 9527
