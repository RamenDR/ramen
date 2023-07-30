# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

sources := $(wildcard \
	drenv \
	*.py \
	envs/*.py \
	addons/*/start \
	addons/*/test \
	addons/*/stop \
	*/deploy \
	*/undeploy \
	*/failover \
	*/relocate \
	*/protect \
	*/unprotect \
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
