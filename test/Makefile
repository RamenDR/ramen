# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# DRIVER can be overriden to allow testing in github when we don't have
# hardware acceleration for VMs.
DRIVER ?= vm

# drenv start timeout in seconds
TIMEOUT ?= 600

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

test: unit-test cluster-test

unit-test:
	$(call run_test,not cluster)

cluster-test:
	$(call run_test,cluster)

define run_test
	rm -f .coverage.*
	COVERAGE_PROCESS_START=.coveragerc python3 -m coverage run -m pytest -m "$1"
	python3 -m coverage combine --append --quiet
endef

coverage:
	python3 -m coverage report

coverage-html:
	python3 -m coverage html
	xdg-open htmlcov/index.html

coverage-clean:
	rm -f .coverage

cluster:
	drenv start --name-prefix $(prefix) $(env) --verbose --timeout $(TIMEOUT)

clean:
	drenv delete --name-prefix $(prefix) $(env)
