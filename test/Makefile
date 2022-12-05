all: flake8 test coverage

flake8:
	python3 -m flake8 drenv */start */test

test:
	python3 -m coverage run --source drenv -m pytest

coverage:
	python3 -m coverage report

coverage-html:
	python3 -m coverage html
	xdg-open htmlcov/index.html
