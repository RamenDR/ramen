all: test coverage

test:
	python3 -m coverage run --source drenv -m pytest

coverage:
	python3 -m coverage report

coverage-html:
	python3 -m coverage html
	xdg-open htmlcov/index.html
