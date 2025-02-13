# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

sources := $(wildcard \
	ramendev \
	*.py \
)

all: flake8 pylint black

flake8:
	python3 -m flake8 $(sources)

pylint:
	python3 -m pylint --errors-only $(sources)

black:
	python3 -m black --check --diff $(sources)

black-reformat:
	python3 -m black $(sources)
