# Build the cloud db factory vertical scaling
ARG DOCKER_BUILD_IMAGE=golang:1.14.4
ARG DOCKER_BASE_IMAGE=alpine:3.12

FROM ${DOCKER_BUILD_IMAGE} AS build
WORKDIR /cloud-thanos-store-discovery/
COPY . /cloud-thanos-store-discovery/
RUN apt-get update -yq && apt-get install -yq unzip
RUN make build


# Final Image
FROM ${DOCKER_BASE_IMAGE}
LABEL name="Mattermost Cloud Database Factory Vertical Scaling" \
  maintainer="cloud-team@mattermost.com" \
  vendor="Mattermost" \
  distribution-scope="public" \
  architecture="x86_64" \
  url="https://mattermost.com" \
  io.k8s.description="Mattermost Cloud Database Factory Vertical Scaling is responsible for RDS cluster scaling" \
  io.k8s.display-name="Mattermost Cloud Database Factory"

ENV CLOUD=/cloud-db-factory-vertical-scaling/main \
    USER_UID=10001 \
    USER_NAME=cloud

RUN  apk update && apk add libc6-compat && apk add ca-certificates
COPY --from=build /cloud-db-factory-vertical-scaling/build/_output/bin/main /cloud-db-factory-vertical-scaling/main
COPY --from=build /cloud-db-factory-vertical-scaling/build/bin /usr/local/bin

RUN  /usr/local/bin/user_setup
WORKDIR /cloud-db-factory-vertical-scaling/

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
