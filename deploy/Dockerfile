# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Runtime image for wardlockd. GoReleaser builds the static binary and puts it
# under <os>/<arch>/ in the build context (.goreleaser.yaml, dockers_v2); this
# file only packages it. Data lives in the /data volume.
# Distroless has no shell, so create the data directory, owned by the
# nonroot user (65532), in a throwaway stage. A new volume inherits it.
FROM busybox:1.37 AS data
RUN mkdir /data && chown 65532:65532 /data && chmod 0700 /data

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY --from=data --chown=65532:65532 /data /data
COPY $TARGETPLATFORM/wardlockd /usr/local/bin/wardlockd
ENV WARDLOCK_DATA_DIR=/data \
    WARDLOCK_HTTP_ADDR=:8080 \
    WARDLOCK_LOG_FORMAT=json
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/wardlockd"]
CMD ["serve"]
