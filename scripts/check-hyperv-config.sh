#!/usr/bin/env bash
set -euo pipefail
# Only local syntax and provider schema checks. No VM lifecycle or SSH calls.
terraform -chdir=cluster/hyperv/terraform fmt -check
terraform -chdir=cluster/hyperv/terraform init -backend=false -input=false
terraform -chdir=cluster/hyperv/terraform validate
python3 -m py_compile cluster/hyperv/bootstrap/prepare.py
ansible-playbook -i cluster/hyperv/ansible/inventory.example.yml cluster/hyperv/ansible/site.yml --syntax-check
ansible-playbook -i cluster/hyperv/ansible/inventory.example.yml cluster/hyperv/ansible/deploy.yml --syntax-check
