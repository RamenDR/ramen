# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# flake8: noqa

import setuptools

with open("README.md", "r") as f:
    long_description = f.read()

setuptools.setup(
    name="ramenctl",
    version="0.1.0",
    author="Nir Soffer",
    author_email="nsoffer@redhat.com",
    description="Tool for developing ramen",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/RamenDR/ramen/ramenctl",
    packages=["ramenctl"],
    install_requires=[
        "PyYAML",
        "drenv",
    ],
    classifiers=[
        "Development Status :: 4 - Beta",
        "Environment :: Console",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: Apache Software License",
        "Operating System :: POSIX :: Linux",
        "Programming Language :: Python :: 3",
        "Topic :: Software Development :: Testing",
    ],
    entry_points={
        "console_scripts": ["ramenctl=ramenctl.__main__:main"],
    },
)
