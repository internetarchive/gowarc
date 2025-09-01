#!/usr/bin/env python3
import json
import mmap
import sys
import zlib

MAGIC = b"\x1f\x8b\x08"  # gzip magic + deflate method


def find_gzip_members(path):
    """
    Return [(offset, length), ...] for each gzip member embedded in the file.
    Complexity is ~O(n): we jump between headers and let zlib tell us where
    each member ends. CRC/ISIZE are validated by zlib automatically.
    """
    members = []

    with open(path, "rb") as f:
        try:
            buf = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
            is_mmap = True
        except (ValueError, OSError):
            buf = f.read()  # fallback for non-regular files
            is_mmap = False

        n = len(buf)
        i = 0

        while True:
            j = buf.find(MAGIC, i)
            if j == -1:
                break

            # Quick sanity: flags byte exists and reserved bits (5..7) must be 0.
            if j + 4 <= n:
                flags = buf[j + 3]
                if flags & 0xE0:
                    i = j + 1
                    continue

            d = zlib.decompressobj(16 + zlib.MAX_WBITS)  # gzip wrapper, checks CRC/ISIZE
            tail = buf[j:n]

            try:
                # Parse without materializing output
                d.decompress(tail, 0)
                if not d.eof:
                    # Truncated/invalid stream at j
                    i = j + 1
                    continue

                consumed = len(tail) - len(d.unused_data)
                if consumed >= 18:  # minimal gzip member size
                    members.append((j, consumed))
                    i = j + consumed   # jump past this member
                else:
                    i = j + 1
            except zlib.error:
                i = j + 1

        if is_mmap:
            buf.close()

    return members


def main():
    if len(sys.argv) != 2:
        print("Usage: python gzip_blob_finder.py <filename>", file=sys.stderr)
        sys.exit(1)
    path = sys.argv[1]
    members = find_gzip_members(path)
    print(json.dumps([{"offset": off, "length": ln} for off, ln in members]))


if __name__ == "__main__":
    main()
