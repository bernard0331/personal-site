# faultserver: one static Go binary on distroless, non-root (like home's).
FROM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod ./
COPY tools/faultserver ./tools/faultserver
RUN CGO_ENABLED=0 go build -trimpath -o /out/faultserver ./tools/faultserver

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/faultserver /faultserver
USER 10001:10001
ENTRYPOINT ["/faultserver"]
