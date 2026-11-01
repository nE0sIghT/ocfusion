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
#include "crypto/sha.h"
#include "crypto/aes-gcm.h"
#include "crypto/aes-ctr.h"

struct params_s params;
volatile sig_atomic_t bQuit;

static bool quic_passthrough = true;
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

static void expand_label(const uint8_t *secret, const char *label,
    uint8_t *output, size_t length)
{
    uint8_t info[80];
    size_t size = strlen(label);
    info[0] = length >> 8;
    info[1] = length;
    info[2] = size;
    memcpy(info + 3, label, size);
    info[3 + size] = 0;
    assert(!hkdfExpand(SHA256, secret, 32, info, size + 4, output, length));
}

static size_t crypto_frame(uint8_t *frame, size_t offset, size_t length)
{
    frame[0] = 6;
    frame[1] = 0x40 | (offset >> 8);
    frame[2] = offset;
    frame[3] = 0x40 | (length >> 8);
    frame[4] = length;
    memcpy(frame + 5, fake_tls_clienthello_default + 5 + offset, length);
    return length + 5;
}

// RFC 9001 client Initial, AES-GCM payload and AES header protection.
static void initial_packet(uint8_t *wire, const uint8_t *frames, size_t length, int number)
{
    const uint8_t salt[20] = {0x38,0x76,0x2c,0xf7,0xf5,0x59,0x34,0xb3,0x4d,0x17,
        0x9a,0xe6,0xa4,0xc8,0x0c,0xad,0xcc,0xbb,0x7f,0x0a};
    const uint8_t cid[8] = {1,2,3,4,5,6,7,8};
    uint8_t secret[32], client[32], key[16], iv[12], hp[16], clear[1162] = {0};
    uint8_t mask[16], tag[16];
    assert(length <= sizeof(clear));
    assert(!hkdfExtract(SHA256, salt, sizeof(salt), cid, sizeof(cid), secret));
    expand_label(secret, "tls13 client in", client, sizeof(client));
    expand_label(client, "tls13 quic key", key, sizeof(key));
    expand_label(client, "tls13 quic iv", iv, sizeof(iv));
    expand_label(client, "tls13 quic hp", hp, sizeof(hp));
    memset(wire, 0, 1200);
    wire[0] = 0xc3;
    wire[4] = 1;
    wire[5] = sizeof(cid);
    memcpy(wire + 6, cid, sizeof(cid));
    size_t protected_length = 1200 - 18;
    wire[16] = 0x40 | (protected_length >> 8);
    wire[17] = protected_length;
    wire[21] = number;
    iv[11] ^= number;
    memcpy(clear, frames, length);
    assert(!aes_gcm_crypt(AES_ENCRYPT, wire + 22, clear, sizeof(clear), key,
        sizeof(key), iv, sizeof(iv), wire, 22, tag, sizeof(tag)));
    memcpy(wire + 1184, tag, sizeof(tag));
    aes_context aes;
    assert(!aes_setkey(&aes, 1, hp, sizeof(hp)));
    assert(!aes_cipher(&aes, wire + 22, mask));
    wire[0] ^= mask[0] & 15;
    for (size_t i = 0; i < 4; i++) {
        wire[18 + i] ^= mask[1 + i];
    }
}

static uint8_t feed_udp(bool ipv6, const uint8_t *data)
{
    // Reuse the address construction from the TCP fixture.
    feed(ipv6, false, TH_SYN, 1000, NULL, 0);
    size_t offset = ipv6 ? 40 : 20;
    if (ipv6) {
        ((struct ip6_hdr *)packet)->ip6_nxt = IPPROTO_UDP;
        ((struct ip6_hdr *)packet)->ip6_plen = htons(1208);
    } else {
        ((struct ip *)packet)->ip_p = IPPROTO_UDP;
        ((struct ip *)packet)->ip_len = htons(1228);
    }
    struct udphdr *udp = (struct udphdr *)(packet + offset);
    memset(udp, 0, sizeof(*udp));
    udp->uh_sport = htons(12345);
    udp->uh_dport = htons(443);
    udp->uh_ulen = htons(1208);
    memcpy(packet + offset + 8, data, 1200);
    size_t size = sizeof(modified);
    return dpi_desync_packet(0, "lan0", "wan0", packet, offset + 1208,
        modified, &size) & VERDICT_MASK;
}

static void check_quic(bool ipv6, bool selected, int layout)
{
    init_params(&params);
    params.quic_initial_passthrough = quic_passthrough;
    assert(lua_init());
    assert(!luaL_dostring(params.L, "probe_count=0; function observe(ctx,d) probe_count=probe_count+1 end"));
    ConntrackPoolInit(&params.conntrack, 10, 60, 300, 60, 60);
    struct desync_profile_list *profile = dp_list_add(&params.desync_profiles);
    profile->dp.filter_ipv4 = profile->dp.filter_ipv6 = true;
    profile->dp.filter_l7 = 1ULL << L7_QUIC;
    assert(funclist_add_tail(&profile->dp.lua_desync, "observe"));
    char path[] = "/tmp/ocf-quic-hostlist-XXXXXX";
    int fd = mkstemp(path);
    assert(fd >= 0);
    const char *name = selected ? "www.microsoft.com\n" : "unrelated.example\n";
    assert(write(fd, name, strlen(name)) == (ssize_t)strlen(name));
    close(fd);
    assert(RegisterHostlist(&profile->dp, false, path));
    assert(LoadAllHostLists());
    struct desync_profile_list *fallback = dp_list_add(&params.desync_profiles);
    fallback->dp.filter_ipv4 = fallback->dp.filter_ipv6 = true;
    uint8_t frames[1200], wire[1200];
    size_t length = crypto_frame(frames, layout == 3 ? 300 : 0, layout == 0 ? 200 : 60);
    if (layout == 2) {
        length += crypto_frame(frames + length, 300, 100);
    }
    initial_packet(wire, frames, length, 1);
    uint8_t clean[1200];
    size_t clean_length = sizeof(clean);
    assert(QUICDecryptInitial(wire, sizeof(wire), clean, &clean_length));
    send_calls = 0;
    assert(feed_udp(ipv6, wire) == VERDICT_PASS);
    assert(send_calls == 0);
    assert(!memcmp(packet + (ipv6 ? 40 : 20) + 8, wire, sizeof(wire)));
    lua_getglobal(params.L, "probe_count");
    assert(lua_tointeger(params.L, -1) == (selected && layout == 0 ? 1 : 0));
    lua_pop(params.L, 1);
    if (layout == 4) {
        assert(feed_udp(ipv6, wire) == VERDICT_PASS);
        assert(send_calls == 0);
    }
    if (layout != 0) {
        length = crypto_frame(frames, layout == 3 ? 0 : 60, 200);
        initial_packet(wire, frames, length, 2);
        assert(feed_udp(ipv6, wire) == VERDICT_PASS);
        assert(send_calls == 0);
    }
    struct dissect dis;
    t_ctrack *track;
    bool reverse;
    proto_dissect_l3l4(packet, (ipv6 ? 40 : 20) + 1208, &dis, false);
    assert(ConntrackPoolDoubleSearch(&params.conntrack, &dis, &track, &reverse));
    assert(rawpacket_queue_count(&track->delayed) == 0);
    lua_getglobal(params.L, "probe_count");
    assert(lua_tointeger(params.L, -1) == (selected ? 1 : 0));
    lua_pop(params.L, 1);
    cleanup_params(&params);
    unlink(path);
}

int main(int argc, char **argv)
{
    if (argc > 1 && !strcmp(argv[1], "--legacy-quic")) {
        quic_passthrough = false;
    }
    for (int ipv6 = 0; ipv6 <= 1; ipv6++) {
        for (int layout = 0; layout < 5; layout++) {
            check_quic(ipv6, false, layout);
            check_quic(ipv6, true, layout);
        }
        check(ipv6, false, 680, VERDICT_PASS);
        check(ipv6, false, 200, VERDICT_PASS);
        check(ipv6, true, 200, VERDICT_DROP);
        check(ipv6, false, 60, VERDICT_DROP);
    }

    puts("PASS: TLS selection/replay and encrypted QUIC observation without holding originals (IPv4/IPv6)");
    return 0;
}
