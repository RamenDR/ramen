# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# DRIVER can be overriden to allow testing in github when we don't have
# hardware acceleration for VMs.
DRIVER ?= vm

env := envs/$(DRIVER).yaml
prefix := drenv-test-

sources := $(wildcard \
	drenv \
	*.py \
	envs/*.py \
	addons/*/start \
	addons/*/test \
	addons/*/stop \
	*/enable-dr \
	*/disable-dr \
	*/deploy \
	*/undeploy \
	*/failover \
	*/relocate \
)

all: flake8 pylint black test coverage

flake8:
	python3 -m flake8 $(sources)

pylint:
	python3 -m pylint --errors-only $(sources)

black:
	python3 -m black --check --diff $(sources)

black-reformat:
	python3 -m black $(sources)

test:
	rm -f .coverage.*
	COVERAGE_PROCESS_START=.coveragerc python3 -m coverage run -m pytest
	python3 -m coverage combine --quiet

coverage:
	python3 -m coverage report

coverage-html:
	python3 -m coverage html
	xdg-open htmlcov/index.html

cluster:
	drenv start --name-prefix $(prefix) $(env) -v

clean:
	drenv delete --name-prefix $(prefix) $(env)
