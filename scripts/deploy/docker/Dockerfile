# scripts/deploy/docker/Dockerfile
# CA source: keel-certs (Dockerfile.certs; build it first with make certs-image).
ARG CERTS_IMAGE=keel-certs:latest
FROM ${CERTS_IMAGE} AS certs

FROM golang:1.27.3 AS builder
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

# Trusted roots: an explicit allowlist, not the full bundle. Go reads every PEM in
# /etc/ssl/certs/. Keep this list identical in Dockerfile.release.
#   ISRG X1/X2 — Let's Encrypt; DigiCert G2/G3 — VeriSign/Symantec successor;
#   D-TRUST (Bundesdruckerei) and Telekom Security — Germany.
COPY --from=certs \
  /certs/ISRG_Root_X1.crt \
  /certs/ISRG_Root_X2.crt \
  /certs/DigiCert_Global_Root_G2.crt \
  /certs/DigiCert_Global_Root_G3.crt \
  /certs/D-TRUST_BR_Root_CA_1_2020.crt \
  /certs/D-TRUST_BR_Root_CA_2_2023.crt \
  /certs/D-TRUST_EV_Root_CA_1_2020.crt \
  /certs/D-TRUST_EV_Root_CA_2_2023.crt \
  /certs/Telekom_Security_TLS_ECC_Root_2020.crt \
  /certs/Telekom_Security_TLS_RSA_Root_2023.crt \
  /etc/ssl/certs/

COPY --from=builder /out/keel /keel

# Run as numeric non-root (no /etc/passwd needed)
USER 65532:65532

EXPOSE 8080 9091 9092
ENTRYPOINT ["/keel"]
