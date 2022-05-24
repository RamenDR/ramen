# Testing

## **Under construction**

## Using interfaces to mock in testing

It is always useful to run the unit or integration tests without setting up
external dependencies. If the communication to the external components is done
through interfaces, it becomes easy to mock those components by using a fake or
mock implementation of those dependencies which satisfy those interfaces in the
tests.

![](interfaces.png?raw=true)

The above picture shows the interfaces that are used in Ramen today.