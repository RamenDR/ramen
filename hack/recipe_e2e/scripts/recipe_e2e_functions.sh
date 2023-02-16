#!/bin/bash
# functions used across recipe e2e scripts

# wait for a file to exist in minio s3 storage, by input path
function wait_for_mc_file() {
    local TIMEOUT_MAX=120
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5
    local FILE=${*:3}  #  usage: 'mc find FILE'

    set +e

    while ((TIMEOUT > 0)); do 
        if [[ $("${@}" | wc -l) -gt 0 ]]; then
            echo "file '$FILE' exists"
            set -e
            return 0
        fi
        echo "file '$FILE' does not yet exist. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    set -e
    echo "file '$FILE' was not created within $TIMEOUT_MAX seconds"
    exit 1
}

# is_backup_successful FILE (full path to json object)
function is_backup_successful() {
    FILE=$1
    
    # shellcheck disable=SC2086
    RESULT=$(mc cat $FILE | grep '"phase":' | awk '{print $2}')
    if [[ "$RESULT" == '"Completed",' ]]; then
        COMPLETED=1
    else
        COMPLETED=0
    fi

    # test for backup objects > 0
    BACKUP_INFO=$(mc cat "$FILE" | grep itemsBackedUp)
    ITEMS=$(echo "$BACKUP_INFO" | awk '{print $2}')

    if [[ $COMPLETED -eq 1 && $ITEMS -gt 0 ]]; then
        echo 1
    else
        echo 0
        exit 1
    fi    
}

# is_hook_successful FILE (full path to json object)
function is_restore_hook_successful() {
    FILE=$1

    # shellcheck disable=SC2086
    RESULT=$(mc cat $FILE | grep '"phase":' | awk '{print $2}')
    if [[ "$RESULT" == '"Completed",' ]]; then
        echo "1"
        return 0
    else
        echo "0"
        echo "restore hook was not successful"
        return 1
    fi
}

function wait_for_and_check_backup_success() {
    wait_for_mc_file mc find "$1"
    is_backup_successful "$1"

    echo "$1 was created and backed up items successfully"
}

function wait_for_vrg_state() {
    local STATE=$1
    local RESOURCE=${*:2}

    local TIMEOUT_MAX=300
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5

    while ((TIMEOUT > 0)); do 
        # shellcheck disable=SC2086
        RESULT=$(kubectl get $RESOURCE -o=jsonpath='{.status.state}')

        if [[ "$RESULT" == "$STATE" ]]; then
            echo "'$RESOURCE' found in $STATE state"
            return 0
        fi
        echo "$RESOURCE not in $STATE state. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    echo "$RESOURCE' did not gain $STATE state within $TIMEOUT_MAX seconds"
    exit 1    
}

function wait_for_pv_unbound() {
    local RESOURCE=${*}

    local TIMEOUT_MAX=60
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5

    while ((TIMEOUT > 0)); do 
        STATE=$(kubectl get pv/"$RESOURCE" --no-headers | awk '{print $5}')

        if [[ "$STATE" == "Available" || "$STATE" == "Released" ]]; then
            echo "'$RESOURCE' found in $STATE state"
            return 0
        fi
        echo "$RESOURCE in $STATE state. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    echo "$RESOURCE' did not become Available or Released within $TIMEOUT_MAX seconds"
    exit 1    
}

function get_restore_index() {
    if [[ $(mc ls "$1"/0/velero/restores | wc -l) -gt 0 ]]; then
        echo "0"
        exit 0
    fi 

    if [[ $(mc ls "$1"/1/velero/restores | wc -l) -gt 0 ]]; then
        echo "1"
        exit 0
    fi 

    echo "couldn't find restores"
    exit 1
}

function verify_restore_success() {
    FILE=$1
    
    # clear existing results
    if [[ -e /tmp/$FILE ]]; then 
        rm /tmp/"$FILE"
    fi

    # log files are compressed; unzip them
    mc cp "$FILE" /tmp/"$FILE".gz
    gunzip /tmp/"$FILE".gz

    # grep for "grep -e "Restored [0-9]* items" -o | awk '{print $2}'" and check > 0
    RESTORED_ITEMS=$(grep /tmp/restore_file -e "Restored [0-9]* items" -o | awk '{print $2}')

    if [[ $RESTORED_ITEMS -gt 0 ]]; then
        echo "$FILE restore success: found $RESTORED_ITEMS restored items"
    else
        echo "$FILE restore failed. Could not find restored items."
        exit 1
    fi
}

function wait_for_resource_creation() {
    local RESOURCE=${*}

    local TIMEOUT_MAX=60
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5

    while ((TIMEOUT > 0)); do 
        # shellcheck disable=SC2086
        COUNT=$(kubectl get $RESOURCE --no-headers | wc -l)
        if [[ $COUNT -gt 0 ]]; then
            echo "resource '$RESOURCE' exists"
            return 0
        fi
        echo "Found $COUNT resources with 'kubectl get $RESOURCE'. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    echo "$RESOURCE' doesn't exist after $TIMEOUT_MAX seconds"
    exit 1    
}

function wait_for_resource_deletion() {
    local RESOURCE=${*}

    local TIMEOUT_MAX=120
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5

    while ((TIMEOUT > 0)); do 
        # shellcheck disable=SC2086
        COUNT=$(kubectl get $RESOURCE --no-headers | wc -l)
        if [[ $COUNT -eq 0 ]]; then
            echo "resource '$RESOURCE' deleted"
            return 0
        fi
        echo "Found $COUNT resources with 'kubectl get $RESOURCE'. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    echo "$RESOURCE' still exists after $TIMEOUT_MAX seconds"
    exit 1
}

function remove_ramen_finalizers() {
    local RESOURCE=${*}
    echo "removing Ramen finalizers from $RESOURCE"
    KUBE_EDITOR="sed -i 's|- volumereplicationgroups.ramendr.openshift.io/pvc-vr-protection||g'" kubectl edit "$RESOURCE"
    KUBE_EDITOR="sed -i 's|- volumereplicationgroups.ramendr.openshift.io/vrg-protection||g'" kubectl edit "$RESOURCE"
}

# usage: wait_for_vrg_condition_status index condition resource
function wait_for_vrg_condition_status() {
    local CONDITION_INDEX=$1
    local CONDITION_STATUS=$2
    local RESOURCE=${*:3}

    local TIMEOUT_MAX=600
    local TIMEOUT=$TIMEOUT_MAX
    local INTERVAL=5

    while ((TIMEOUT > 0)); do 
        # shellcheck disable=SC2086
        RESULT=$(kubectl get $RESOURCE -o=jsonpath="{.status.conditions[$CONDITION_INDEX].status}")
        if [[ "$RESULT" == "$CONDITION_STATUS" ]]; then
            echo "$RESOURCE has condition $CONDITION_INDEX status $CONDITION_STATUS"
            return 0
        fi
        echo "$RESOURCE has condition $CONDITION_INDEX status $RESULT. Waiting $INTERVAL seconds..."
        sleep $INTERVAL
        TIMEOUT=$((TIMEOUT - INTERVAL))
    done

    echo "$RESOURCE' condition $CONDITION_INDEX did not achieve $CONDITION_STATUS after $TIMEOUT_MAX seconds"
    exit 1
}
