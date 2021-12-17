## CI configurations and examples
This folder holds vendors CI configurations and examples. Configurations are used to control the vendors CI behaviors, and examples are used as a reference for other vendors to be able to setup a CI of their own.

### Admin list
The admin list contains the list of github users and organizations that have permission to trigger the vendors CI. Only trusted users who have merge permissions should be on the list. The vendors should be responsible for how the admin list on their CI is updated, but to keep everything organized the vendors CI should at least update their admin list in response to a PR comment with the following phrase `/update-admins`.

### CI Examples
The examples folder contains configuration examples for vendor CI. It can be used as a reference for vendors to setup their CI. For more information on an example refer to that folder README.

### Vendors CI triggers convention
A vendor CI trigger phrases should follow the following convention:

```
/test-<test-type>-<vendor>-<sub-test>
/skip-<test-type>-<vendor>-<sub-test>
```

where:
 1. `test-type`: The type of test to conduct on the vendor's setups, for example: e2e. It can be replaced with `all` to test all tests types and vendors, currently the following values are supported:
 * `e2e`: Runs the project's e2e make rule on the specific vendor setup.
 * `all`: Runs all test types and all their tests for all vendors.

 2. `vendor`: The vendor that implemented the CI, currently the following values are supported:
 * `nvidia`: Runs tests implemented by NVIDIA, the following test types are supported: e2e.
 * `all`: Runs the specified test type and all its sub tests for all vendors.

 3. `sub-test`: In case there are many tests for the `test-type`, this field specify what sub-test to run. currently the following values are supported:
 * `all`: Runs all subtest of the specified vendor.

Note that *all fields are required* except if `all` is added to the phrase, in that case, there must not be any field after the `all`. This means that the following phrases are supported when `all` is used:
 1. `/test-all`
 2. `/test-<test-type>-all`
 3. `/test-<test-type>-<vendor>-all`

But the following are not:
 1. `/test-all-<vendor>`
 2. `/test-all-<vendor>-<sub-test>`
 3. `/test-<test-type>-all-<sub-test>`

The skip would report pass without running the test, and its triggers follow the above convention as well.

