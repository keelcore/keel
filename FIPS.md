### FIPS Compliance Note
Keel currently supports a **FIPS Compatibility Mode**.

When compiled with the `boringcrypto` experiment or linked against Google's BoringSSL
infrastructure, Keel utilizes FIPS-validated cryptographic modules.

**Status**: Currently "FIPS Compatible, currently not certified." Formal FIPS 140-2/3
certification of the Keel distribution itself is on the roadmap.
