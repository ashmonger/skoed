Feature: Block policy configuration
  As a network administrator
  I want to configure what DNS response dblock returns for blocked domains
  So that I can control client behavior on blocked queries

  Supported block policy values:
    - NXDOMAIN: domain does not exist
    - NULL: A=0.0.0.0, AAAA=::
    - NODATA: NOERROR with empty answer section

  Non-goals:
    - Custom block page hosting is out of scope for Milestone 1
    - Silent drop (no response) is not a supported policy
    - Per-client block policy is out of scope (Milestone 3)

  @fsid:FS-BlockPolicyConfigurationGlobalDefault
  Scenario: Admin sets the global default block policy
    When the admin sets the global block policy to "NXDOMAIN"
    Then all blocked domain queries return NXDOMAIN
    Unless a per-blocklist policy overrides it

  @fsid:FS-BlockPolicyConfigurationPerBlocklist
  Scenario: Admin sets a block policy on a specific blocklist
    Given the global block policy is "NXDOMAIN"
    When the admin sets the block policy on blocklist "ads" to "NULL"
    Then queries for domains in "ads" return NULL (A=0.0.0.0, AAAA=::)
    And queries for domains in other blocklists still return NXDOMAIN

  @fsid:FS-BlockPolicyConfigurationChangeGlobal
  Scenario: Admin changes the global block policy
    Given the global block policy is "NXDOMAIN"
    And no per-blocklist policy overrides are set
    When the admin changes the global block policy to "NODATA"
    Then all blocked domain queries return NOERROR with an empty answer section

  @fsid:FS-BlockPolicyConfigurationPerBlocklistReset
  Scenario: Admin removes a per-blocklist policy override
    Given blocklist "ads" has a per-blocklist policy of "NULL"
    And the global block policy is "NXDOMAIN"
    When the admin removes the per-blocklist policy on "ads"
    Then queries for domains in "ads" return NXDOMAIN (inherits global default)
