# Build the cloud thanos store discovery
ARG DOCKER_BUILD_IMAGE=golang:1.17.3
ARG DOCKER_BASE_IMAGE=alpine:3.14

FROM ${DOCKER_BUILD_IMAGE} AS build
WORKDIR /cloud-thanos-store-discovery/
COPY . /cloud-thanos-store-discovery/
RUN apt-get update -yq && apt-get install -yq unzip
RUN make build


# Final Image
FROM ${DOCKER_BASE_IMAGE}
LABEL name="Mattermost Cloud Thanos Store Discovery" \
  maintainer="cloud-team@mattermost.com" \
  vendor="Mattermost" \
  distribution-scope="public" \
  architecture="x86_64" \
  url="https://mattermost.com" \
  io.k8s.description="Mattermost Cloud Thanos Store Discovery is responsible for registering new Thanos Query endpoints" \
  io.k8s.display-name="Mattermost Cloud Thanos Store Discovery"

ENV CLOUD=/cloud-thanos-store-discovery/main \
    USER_UID=10001 \
    USER_NAME=cloud

RUN  apk update && apk add libc6-compat && apk add ca-certificates
COPY --from=build /cloud-thanos-store-discovery/build/_output/bin/main /cloud-thanos-store-discovery/main
COPY --from=build /cloud-thanos-store-discovery/build/bin /usr/local/bin

RUN  /usr/local/bin/user_setup
WORKDIR /cloud-thanos-store-discovery/

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
