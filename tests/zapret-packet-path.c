// SPDX-License-Identifier: GPL-3.0-or-later
#define _GNU_SOURCE
#include <assert.h>
#include <errno.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "params.h"
#include "desync.h"
#include "protocol.h"
#include "lua.h"
#include "hostlist.h"

struct params_s params;
volatile sig_atomic_t bQuit;

static unsigned int send_calls;
static unsigned int failures_left;

bool __wrap_rawsend_rp(const struct rawpacket *rp)
{
    send_calls++;
    if (failures_left) {
        failures_left--;
        errno = ENOBUFS;
        return false;
    }
    return true;
}

bool __wrap_rawsend(const struct sockaddr *dst, uint32_t mark,
    const char *iface, const void *data, size_t len)
{
    return __wrap_rawsend_rp(NULL);
}

static uint8_t packet[4096];
static uint8_t modified[8192];

static uint8_t feed(bool ipv6, bool reverse, uint8_t flags, uint32_t seq,
    const uint8_t *payload, size_t length)
{
    memset(packet, 0, sizeof(packet));
    size_t offset = ipv6 ? 40 : 20;

    if (ipv6) {
        struct ip6_hdr *ip = (struct ip6_hdr *)packet;
        ip->ip6_flow = htonl(6U << 28);
        ip->ip6_plen = htons(20 + length);
        ip->ip6_nxt = IPPROTO_TCP;
        ip->ip6_hlim = 64;
        ip->ip6_src.s6_addr[15] = reverse ? 2 : 1;
        ip->ip6_dst.s6_addr[15] = reverse ? 1 : 2;
    } else {
        struct ip *ip = (struct ip *)packet;
        ip->ip_v = 4;
        ip->ip_hl = 5;
        ip->ip_len = htons(offset + 20 + length);
        ip->ip_p = IPPROTO_TCP;
        ip->ip_ttl = 64;
        ip->ip_src.s_addr = htonl(reverse ? 0xc0000202 : 0xc0000201);
        ip->ip_dst.s_addr = htonl(reverse ? 0xc0000201 : 0xc0000202);
    }

    struct tcphdr *tcp = (struct tcphdr *)(packet + offset);
    tcp->th_sport = htons(reverse ? 443 : 12345);
    tcp->th_dport = htons(reverse ? 12345 : 443);
    tcp->th_seq = htonl(seq);
    tcp->th_ack = htonl(reverse ? 1001 : 2001);
    tcp->th_off = 5;
    tcp->th_flags = flags;
    tcp->th_win = htons(65535);

    if (length) {
        memcpy(packet + offset + 20, payload, length);
    }

    size_t modified_length = sizeof(modified);
    return dpi_desync_packet(0, reverse ? "wan0" : "lan0",
        reverse ? "lan0" : "wan0", packet, offset + 20 + length,
        modified, &modified_length) & VERDICT_MASK;
}

static void check(bool ipv6, bool action, size_t length, uint8_t expected)
{
    init_params(&params);
    assert(lua_init());
    ConntrackPoolInit(&params.conntrack, 10, 60, 300, 60, 60);
    struct desync_profile_list *entry = dp_list_add(&params.desync_profiles);
    assert(entry);
    entry->dp.filter_ipv4 = entry->dp.filter_ipv6 = true;

    char path[] = "/tmp/ocf-hostlist-XXXXXX";
    int fd = mkstemp(path);
    assert(fd >= 0);
    const char *name = action ? "www.microsoft.com\n" : "unrelated.example\n";
    assert(write(fd, name, strlen(name)) == (ssize_t)strlen(name));
    close(fd);
    assert(RegisterHostlist(&entry->dp, false, path));
    assert(funclist_add_tail(&entry->dp.lua_desync, "fake"));
    assert(LoadAllHostLists());
    struct desync_profile_list *fallback = dp_list_add(&params.desync_profiles);
    assert(fallback);
    fallback->dp.filter_ipv4 = fallback->dp.filter_ipv6 = true;

    assert(feed(ipv6, false, TH_SYN, 1000, NULL, 0) == VERDICT_PASS);
    assert(feed(ipv6, true, TH_SYN | TH_ACK, 2000, NULL, 0) == VERDICT_PASS);
    send_calls = 0;
    uint8_t verdict = feed(ipv6, false, TH_ACK, 1001,
        fake_tls_clienthello_default, length);
    assert(verdict == expected);
    assert(send_calls == 0);

    t_ctrack *track;
    bool reverse;
    struct dissect dis;
    proto_dissect_l3l4(packet, (ipv6 ? 60 : 40) + length, &dis, false);
    assert(ConntrackPoolDoubleSearch(&params.conntrack, &dis, &track, &reverse));
    assert((rawpacket_queue_count(&track->delayed) != 0) == (expected == VERDICT_DROP));
    if (!action && length == 60) {
        send_calls = 0;
        failures_left = 1;
        assert(feed(ipv6, false, TH_ACK, 1061,
            fake_tls_clienthello_default + 60, 620) == VERDICT_DROP);
        assert(send_calls == 3);
        assert(failures_left == 0);
        assert(rawpacket_queue_count(&track->delayed) == 0);
    }

    // Avoid raw socket replay during cleanup of the deliberately pending case.
    rawpacket_queue_destroy(&track->delayed);
    cleanup_params(&params);
    unlink(path);
}

int main(int argc, char **argv)
{
    for (int ipv6 = 0; ipv6 <= 1; ipv6++) {
        check(ipv6, false, 680, VERDICT_PASS);
        check(ipv6, false, 200, VERDICT_PASS);
        check(ipv6, true, 200, VERDICT_DROP);
        check(ipv6, false, 60, VERDICT_DROP);
    }

    puts("PASS: unmatched partial TLS preserves originals; selected and incomplete SNI remain buffered (IPv4/IPv6)");
    return 0;
}
