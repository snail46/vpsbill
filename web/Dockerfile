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
# 80/81: portal/admin for direct clients; 8080/8081: behind a trusted proxy.
EXPOSE 80 81 8080 8081
