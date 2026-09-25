FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hooks .

FROM debian:bookworm-slim
COPY --from=build /out/hooks /usr/local/bin/hooks
ENV LISTEN_ADDR=:8090
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/hooks"]
