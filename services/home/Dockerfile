# The landing page: one static Go binary on a distroless base (no shell, no
# package manager), running as the image's non-root user.
FROM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod ./
COPY services/home ./services/home
RUN CGO_ENABLED=0 go build -trimpath -o /out/site ./services/home

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/site /site
USER 10001:10001
ENTRYPOINT ["/site", "-addr=:8080"]
