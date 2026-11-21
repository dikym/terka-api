#!/usr/bin/env bash
# Runs dns-check.sh against recorded DNS answers.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

dir=$(mktemp -d)
trap 'rm -r "$dir"' EXIT

# answer <name> [txt...]: writes a DoH answer whose TXT data is given verbatim, quotes included.
answer() {
	local name=$1
	shift
	jq -n '{Answer: ($ARGS.positional | map({data: .}))}' --args "$@" >"$dir/$name.json"
}
valid() {
	answer send.terka.my.id '"v=spf1 include:amazonses.com ~all"'
	answer resend._domainkey.terka.my.id '"p=MIGfMA0GCSqGSIb3DQEBAQUAA4GN"'
	answer _dmarc.terka.my.id '"v=DMARC1; p=none"'
}

# check <name> <expected output> <expected exit>
check() {
	local out code
	out=$(DNS_FIXTURE_DIR=$dir bash "$here/dns-check.sh") && code=0 || code=$?
	if [ "$out" = "$2" ] && [ "$code" = "$3" ]; then pass "$1"; else fail "$1" "output '$out', exit $code"; fi
}

valid
check "all records are right" "" 0

valid
answer send.terka.my.id '"v=spf1 ~all"'
check "SPF without a mechanism" "SPF on send.terka.my.id does not authorize any sender" 1

valid
echo '{"Status":0}' >"$dir/send.terka.my.id.json"
check "missing SPF" "SPF on send.terka.my.id does not authorize any sender" 1

valid
answer resend._domainkey.terka.my.id
check "empty DKIM" "DKIM key resend._domainkey.terka.my.id is missing" 1

valid
answer _dmarc.terka.my.id
check "empty DMARC" "DMARC record _dmarc.terka.my.id is missing" 1

valid
answer send.terka.my.id '"v=spf1 ip4:1.2.3.4 " "include:amazonses.com ~all"'
answer resend._domainkey.terka.my.id '"p=MIGfMA0GCSqGSIb3" "DQEBAQUAA4GN"'
check "TXT split in two strings is still read" "" 0

[ "$failures" -eq 0 ] || { echo "$failures failed"; exit 1; }
