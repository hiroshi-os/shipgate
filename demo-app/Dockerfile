FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-app .

FROM debian:bookworm-slim
COPY --from=build /out/demo-app /usr/local/bin/demo-app
ENV LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/demo-app"]
