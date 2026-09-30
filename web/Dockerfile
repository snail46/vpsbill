FROM node:24-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm run build

FROM nginx:1.29-alpine
COPY nginx/default.conf /etc/nginx/conf.d/default.conf
COPY nginx/common.conf nginx/portal.conf nginx/admin.conf /etc/nginx/snippets/
COPY --from=build /app/dist /usr/share/nginx/html
# 80/81: portal/admin for direct clients; 7080/7081: behind a trusted proxy
# (8080/8081 still answer as before, for tunnels set up earlier).
EXPOSE 80 81 7080 7081 8080 8081
