#!/usr/bin/env bash
# Checks the email records of the login sender: Resend's SPF on its "send" return-path subdomain,
# its DKIM key and a DMARC policy on the domain. Prints one line per problem and exits 1 if any.
# DNS_FIXTURE_DIR makes it read <name>.json from that folder instead of querying Cloudflare.
set -uo pipefail

domain=terka.my.id

# txt <name>: one TXT record per line. Data arrives quoted and may be split into several quoted strings.
txt() {
	local name=$1
	if [ -n "${DNS_FIXTURE_DIR:-}" ]; then
		cat "$DNS_FIXTURE_DIR/$name.json"
	else
		curl -fsS --max-time 10 -H 'accept: application/dns-json' \
			"https://cloudflare-dns.com/dns-query?name=$name&type=TXT"
	fi | jq -r '.Answer[]?.data | gsub("\"\\s*\""; "") | gsub("\""; "")'
}

spf=$(txt "send.$domain") || { echo "could not read send.$domain"; exit 1; }
dkim=$(txt "resend._domainkey.$domain") || { echo "could not read resend._domainkey.$domain"; exit 1; }
dmarc=$(txt "_dmarc.$domain") || { echo "could not read _dmarc.$domain"; exit 1; }

problems=0
problem() { echo "$1"; problems=1; }

grep '^v=spf1' <<<"$spf" | grep -Eq '(^|[ +])(ip4|ip6|include):' ||
	problem "SPF on send.$domain does not authorize any sender"
grep -q 'p=' <<<"$dkim" || problem "DKIM key resend._domainkey.$domain is missing"
grep -q '^v=DMARC1' <<<"$dmarc" || problem "DMARC record _dmarc.$domain is missing"
exit "$problems"
