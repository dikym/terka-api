# Postgres for local development and CI: the production image plus pgTAP and pg_prove.
FROM postgres:18

RUN apt-get update \
	&& apt-get install -y --no-install-recommends postgresql-18-pgtap libtap-parser-sourcehandler-pgtap-perl \
	&& rm -rf /var/lib/apt/lists/*
