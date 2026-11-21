#!/usr/bin/env bash
# Lint and syntax-check the playbook in the official Ansible tools container.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/deploy/ansible"
docker run --rm -v "$PWD":/work -w /work -e ANSIBLE_COLLECTIONS_PATH=/tmp/c \
	ghcr.io/ansible/community-ansible-dev-tools:latest sh -c \
	'ansible-galaxy collection install -r requirements.yml -p /tmp/c >/dev/null 2>&1 &&
	 ansible-lint -q &&
	 ansible-playbook -i inventory.example.ini site.yml --syntax-check >/dev/null &&
	 echo "ansible-lint and syntax-check ok"'
