# Keel Trademark Policy

## ⚓ The Keel Identity
"Keel" is the exclusive trademark of the **KeelCore** project. While our code is open-source under the **Apache License 2.0**, our name represents a specific standard of "shredded" efficiency and rock-hard security.

## 1. Governance Philosophy
The Keel name is a guarantee of a 2MB standalone footprint and a fail-closed FIPS posture. Governance of the name ensures that forks or downstream distributions do not dilute these core architectural requirements.

## 2. Permitted Use
You **may** use the Keel name without prior written permission for:
* **Compatibility**: "Plugins for Keel" or "Keel-compatible middleware."
* **Education**: "Hardening OCI containers with Keel."
* **Internal Infra**: Running `keel-fips` inside your corporate cluster.

## 3. Restricted Use
You **must** rename your project and may not use "Keel" as the primary brand if:
* **Binary Bloat**: Your fork exceeds the 3MB threshold for minimalist builds.
* **Security Softening**: You disable the "fail-closed" FIPS logic in the `max` build.
* **Commercialization**: You offer "Keel-as-a-Service" or "Keel Enterprise Support" without an agreement.

## 4. The Shred-Check Requirement
If your version of the software does not pass the official build and binary size verification in `./scripts/build/`, it is not "Keel." It is a derivative work and must be labeled as such (e.g., "Generic-Server, based on Keel code").