# Logging

This document outlines the logging standards and best practices for Ramen.

## General Principles

1. **Consistency**: All controllers should follow the same logging conventions.
1. **Performance Awareness**: Logging should not significantly impact the
   performance. Be mindful of the logging levels and the amount of data logged.

## Logger Configuration

- Each controller should create its logger instance with a name specific to the
  controller for easy identification. For example, if the controller is named
  `ProtectedVolumeReplicationGroupList`, the logger name could be a shortened
  form of the controller like `pvrgl`.

  ```go
  logger := ctrl.Log.WithName("pvrgl")
  ```

- Loggers should be enriched with key contextual information to make the logs
  more informative. This includes:
    - The name of the custom resource.
    - Then namespace of the custom resource, if applicable.
    - The UID of the custom resource.
    - The version of the custom resource.

  ```go
  logger = logger.WithValues("name", req.NamespacedName.Name, "rid", instance.ObjectMeta.UID, "version", instance.ObjectMeta.ResourceVersion)
  ```

- The context passed to functions should be augmented with the logger instance:

  ```go
  ctx = ctrl.LoggerInto(ctx, logger)
  ```

  This will be useful when we call functions that are outside of the controller
  scope and the instance.log object is not available to them.

## Log Messages

- Log messages should be concise yet informative. Wherever possible, use
  structured logging to provide context.

- Start and end of significant operations, like reconciliation, should be logged:

  ```go
  logger.Info("reconcile start")
  defer logger.Info("reconcile end", "time spent", time.Since(start))
  ```

- When logging errors, include the context of the error:

  ```go
  logger.Error(err, "Error description", "key", "value")
  ```

- Use key-value pairs instead of concatenating the message and data.

    For example, avoid logging like this:

    ```go
    log.Info(fmt.Sprintf("Requeuing after %v", duration))
    ```

    This approach logs everything as one string, making it harder to parse and filter.

    Log additional information as key-value pairs. This method ensures that
    each piece of information is logged as a separate field, making it easier
    to search and analyze.

    Example of a well-structured log message:

    ```go
    s.log.Info("reconcile end", "time spent", time.Since(start))
    ```

    This will log in the following format, providing clear and structured context:

    ```
    2024-01-31T14:27:12.726-0500  INFO    pvrgl   reconcile end   {"name": "protectedvrglist-0", "rid": "1e09b0fb-687b-4100-9ab1-a52ba899b37b", "rv": "322", "time spent": 6ms}
    ```

## Debugging with Logs

The structured logs help debugging. Here's how you can effectively use the logs
for debugging purposes:

### Filtering Logs for a Specific Resource

To focus on logs related to a specific resource, you can use `grep` with a
pattern that matches the resource's unique identifiers. For example:

```
grep -e 'pvrgl.*4db288b5-3f03-441c-bc44-00e356e77f62'
```

This command filters logs to show entries for the resource with UID
`4db288b5-3f03-441c-bc44-00e356e77f62`.
