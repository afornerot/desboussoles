FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/boussole ./cmd/server
RUN CGO_ENABLED=0 go build -o /out/importcsv ./cmd/importcsv

FROM alpine:3.20
ARG AMOXTLI_VERSION=0.17.3
ADD https://github.com/Bornholm/amoxtli/releases/download/v${AMOXTLI_VERSION}/amoxtli_${AMOXTLI_VERSION}_linux_amd64.tar.gz /tmp/amoxtli.tar.gz
RUN tar -xzf /tmp/amoxtli.tar.gz -C /usr/local/bin amoxtli \
    && chmod +x /usr/local/bin/amoxtli \
    && rm /tmp/amoxtli.tar.gz
RUN adduser -D -u 1000 app \
    && mkdir -p /data/amoxtli \
    && chown app:app /data/amoxtli
USER app
COPY --from=build /out/boussole /usr/local/bin/boussole
COPY --from=build /out/importcsv /usr/local/bin/importcsv
EXPOSE 8080
ENTRYPOINT ["boussole"]
