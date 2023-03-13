all: flake8 pylint black test coverage

venv:
	scripts/make-venv.sh

flake8:
	python3 -m flake8 drenv */start */test

pylint:
	python3 -m pylint --errors-only drenv */start */test

black:
	python3 -m black --check --diff drenv */start */test

black-reformat:
	python3 -m black drenv */start */test

test:
	rm -f .coverage.*
	COVERAGE_PROCESS_START=.coveragerc python3 -m coverage run -m pytest
	python3 -m coverage combine --quiet

coverage:
	python3 -m coverage report

coverage-html:
	python3 -m coverage html
	xdg-open htmlcov/index.html
