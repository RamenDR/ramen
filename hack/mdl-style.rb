# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

all

#Refer below url for more information about the markdown rules.
#https://github.com/markdownlint/markdownlint/blob/master/docs/RULES.md

rule 'MD007', :indent => 4
rule 'MD009', :br_spaces => 2
rule 'MD013', :ignore_code_blocks => true, :tables => false
rule 'MD024', :allow_different_nesting => true

exclude_rule 'MD040' # Fenced code blocks should have a language specified
exclude_rule 'MD041' # First line in file should be a top level header
