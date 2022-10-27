# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2086
git_checkout()
{
        git --git-dir $1/.git --work-tree $1 checkout $2
}
git_checkout_undo()
{
        git_checkout $1 -
}
git_clone_and_checkout()
{
        set +e
        git clone $1/$2
        # fatal: destination path '$2' already exists and is not an empty directory.
        set -e
        git --git-dir $2/.git fetch $1/$2 $3
        git_checkout $2 $4
}
git_branch_delete()
{
        set +e
        git --git-dir $1/.git branch --delete $3 $2
        # error: branch '<branchname>' not found.
        set -e
}
git_branch_delete_force()
{
	git_branch_delete $1 $2 --force
}
git_checkout_unset()
{
	unset -f git_checkout_unset
	unset -f git_branch_delete_force
	unset -f git_branch_delete
	unset -f git_clone_and_checkout
	unset -f git_checkout_undo
	unset -f git_checkout
}
