# scripts/deploy/docker/Dockerfile
FROM golang:1.25.5 AS builder
WORKDIR /src
COPY . .

RUN chmod +x ./scripts/build/ci_min.sh ./scripts/build/ci_max.sh ./scripts/build/ci_max_no_fips.sh

ARG FLAVOR=max
RUN mkdir -p /out && \
    case "${FLAVOR}" in \
      min)  ./scripts/build/ci_min.sh && cp -f dist/keel-min  /out/keel ;; \
      max)  ./scripts/build/ci_max_no_fips.sh && cp -f dist/keel-max  /out/keel ;; \
      fips) ./scripts/build/ci_max.sh && cp -f dist/keel-fips /out/keel ;; \
      *) echo "unknown FLAVOR=${FLAVOR} (use min|max|fips)"; exit 2 ;; \
    esac

# ---- Runtime: scratch, numeric non-root ----
FROM scratch

# Single pinned CA (ONLY if you need outbound TLS).
# Put your CA PEM in-repo at deploy/ca/keel_root_ca.pem (example path)
# COPY deploy/ca/keel_root_ca.pem /etc/ssl/certs/ca.pem
# ENV SSL_CERT_FILE=/etc/ssl/certs/ca.pem

COPY --from=builder /out/keel /keel

# Run as numeric non-root (no /etc/passwd needed)
USER 65532:65532

EXPOSE 8080 9091 9092
ENTRYPOINT ["/keel"]
