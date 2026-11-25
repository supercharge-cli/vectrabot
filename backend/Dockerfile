FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /sandbox-host .

FROM debian:bookworm-slim
COPY --from=build /sandbox-host /usr/local/bin/sandbox-host
RUN useradd --create-home --shell /bin/bash user
USER user
WORKDIR /home/user
ENV VECTRABOT_HOST_ADDR=0.0.0.0:8788
EXPOSE 8788
CMD ["sandbox-host"]
