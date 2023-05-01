#!/bin/bash -e

venv=~/.venv/ramen

if [ -d "$venv" ]; then
    echo "Virtual environemnt exists: '$venv'"
else
    echo "Creating virtual environment: '$venv'"
    python3 -m venv $venv
fi

echo "Creating venv $venv"
python3 -m venv $venv

echo "Upgrading pip..."
$venv/bin/pip install --upgrade pip

echo "Installing drenv..."
$venv/bin/pip install -e .

echo
echo "To activate the environment run:"
echo
echo "    source $venv/bin/activate"
echo
