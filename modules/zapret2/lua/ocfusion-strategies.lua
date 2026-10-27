-- SPDX-License-Identifier: GPL-3.0-or-later

-- Use the pinned engine's fragmenter, retaining the original if sending fails.
-- This is fixed module code; no user-provided Lua or expressions are evaluated.
function ocf_fragment(ctx, desync)
    if not direction_check(desync) or not payload_check(desync) then
        return VERDICT_PASS
    end

    local packet = deepcopy(desync.dis)

    if rawsend_dissect_ipfrag(packet, desync_opts(desync)) then
        return VERDICT_DROP
    end

    return VERDICT_PASS
end
