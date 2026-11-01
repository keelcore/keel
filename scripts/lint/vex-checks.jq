# scripts/lint/vex-checks.jq — OpenVEX checks run by scripts/lint/vex.sh.
# Prints one line per problem; empty output means the document is valid.
def utc: type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z$");
(if ."@context" == "https://openvex.dev/ns/v0.2.0" then empty else "@context is not OpenVEX v0.2.0" end),
(if (.timestamp | utc) then empty else "document timestamp is not RFC 3339 UTC" end),
(if (.statements | type) == "array" and (.statements | length) > 0 then empty else "no statements" end),
(.statements[]? | . as $s | (.vulnerability.name // "<unnamed>") as $v |
  (if (.vulnerability.name // "") != "" then empty else "statement without vulnerability.name" end),
  (if ((.products // []) | length) > 0 then empty else "\($v): no products" end),
  (if (.status | IN("not_affected", "affected", "fixed", "under_investigation")) then empty
   else "\($v): invalid status \(.status)" end),
  (if .status != "not_affected" or ((.justification // .impact_statement // "") != "") then empty
   else "\($v): not_affected needs a justification or impact_statement" end),
  (if .status != "affected" or ((.action_statement // "") != "") then empty
   else "\($v): affected needs an action_statement" end),
  (if ($s.timestamp == null) or ($s.timestamp | utc) then empty else "\($v): timestamp is not UTC" end))
