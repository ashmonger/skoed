Feature: Local DNS entry management
  As a network administrator
  I want to define custom DNS records for hostnames on my home or lab network
  So that clients can resolve internal hostnames without a separate DNS server

  Non-goals:
    - This feature does not cover full authoritative zone management (SOA, NS, MX, TXT, SRV records are out of scope for M1)
    - Local entries do not participate in blocklist or allowlist evaluation; they are always served
    - DNSSEC signing of local entries is out of scope

  Background:
    Given skoed is running

  @fsid:FS-LocalDnsEntryAddA
  Scenario: Admin adds an A record for an internal hostname
    When the admin adds a local A record: hostname "nas.home", address "192.168.1.50", TTL 300
    Then a client querying "nas.home" receives an A record with address "192.168.1.50"
    And the TTL in the response is 300

  @fsid:FS-LocalDnsEntryAddAAAA
  Scenario: Admin adds an AAAA record for an internal hostname
    When the admin adds a local AAAA record: hostname "nas.home", address "fd00::50", TTL 300
    Then a client querying "nas.home" for AAAA receives the address "fd00::50"

  @fsid:FS-LocalDnsEntryAddCNAME
  Scenario: Admin adds a CNAME record pointing to another hostname
    When the admin adds a local CNAME record: hostname "files.home", target "nas.home"
    And a local A record exists for "nas.home" with address "192.168.1.50"
    Then a client querying "files.home" receives a CNAME record pointing to "nas.home"
    And the response includes the A record for "nas.home" in the additional section

  @fsid:FS-LocalDnsEntryPriorityOverUpstream
  Scenario: Local entry is served instead of forwarding to upstream
    Given a local A record exists for "nas.home" with address "192.168.1.50"
    When a client queries "nas.home"
    Then skoed returns the local record without contacting any upstream resolver

  @fsid:FS-LocalDnsEntryPriorityOverBlocklist
  Scenario: Local entry is served even when the hostname is on a blocklist
    Given a local A record exists for "intranet.home" with address "10.0.0.1"
    And "intranet.home" is on an active blocklist
    When a client queries "intranet.home"
    Then skoed returns the local A record
    And does not return a block response

  @fsid:FS-LocalDnsEntryUpdate
  Scenario: Admin updates an existing local entry
    Given a local A record exists for "nas.home" with address "192.168.1.50"
    When the admin updates the A record for "nas.home" to address "192.168.1.51"
    Then a client querying "nas.home" receives the address "192.168.1.51"

  @fsid:FS-LocalDnsEntryDelete
  Scenario: Admin deletes a local entry
    Given a local A record exists for "nas.home" with address "192.168.1.50"
    When the admin deletes the local entry for "nas.home"
    Then a client querying "nas.home" receives a response from upstream resolution (not the deleted local record)

  @fsid:FS-LocalDnsEntryNxdomainWhenNoUpstream
  Scenario: Deleted local entry for a non-existent upstream domain returns NXDOMAIN
    Given a local A record for "internal.home" is deleted
    And "internal.home" does not exist in upstream DNS
    When a client queries "internal.home"
    Then skoed returns NXDOMAIN
