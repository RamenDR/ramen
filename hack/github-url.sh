#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

github_url_file()
{
        echo https://raw.githubusercontent.com/"$1"/"$3"/"$2"
}
github_url_directory()
{
        echo https://github.com/"$1"/"$2"?ref="$3"
}
github_url_unset()
{
	unset -f github_url_file
	unset -f github_url_directory
	unset -f github_url_unset
}
