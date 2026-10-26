// Read DEX files from a stopped research process via /proc/PID/mem (no injection).
#define _FILE_OFFSET_BITS 64
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc != 3) { fprintf(stderr, "usage: %s PID OUTDIR\n", argv[0]); return 1; }
    char name[256], line[2048];
    snprintf(name, sizeof(name), "/proc/%d/maps", atoi(argv[1]));
    FILE *maps = fopen(name, "r");
    snprintf(name, sizeof(name), "/proc/%d/mem", atoi(argv[1]));
    int mem = open(name, O_RDONLY);
    if (!maps || mem < 0) { perror("open process"); return 2; }
    mkdir(argv[2], 0700);
    const size_t chunk = 1024*1024;
    unsigned char *buf = malloc(chunk + 112);
    int count = 0;
    while (fgets(line, sizeof(line), maps)) {
        unsigned long long start, end;
        char protection[5];
        if (sscanf(line, "%llx-%llx %4s", &start, &end, protection) != 3 || protection[0] != 'r') continue;
        if (strstr(line,"/system/") || strstr(line,"/apex/") || strstr(line,"/vendor/") || strstr(line,"/dev/")) continue;
        for (uint64_t pos = start; pos < end; pos += chunk) {
            size_t wanted = end-pos < chunk+112 ? end-pos : chunk+112;
            ssize_t got = pread(mem, buf, wanted, pos);
            if (got < 112) continue;
            for (size_t j = 0; j+112 <= (size_t)got && j < chunk; j++) {
                if (memcmp(buf+j,"dex\n0",5) || buf[j+7] != 0) continue;
                uint32_t size, header, endian, classes;
                memcpy(&size,buf+j+32,4); memcpy(&header,buf+j+36,4);
                memcpy(&endian,buf+j+40,4); memcpy(&classes,buf+j+96,4);
                if (header != 112 || endian != 0x12345678 || size < 112 || size > 300*1024*1024) continue;
                uint64_t address = pos+j;
                snprintf(name,sizeof(name),"%s/%llx-%u.dex",argv[2],(unsigned long long)address,size);
                FILE *out = fopen(name,"wb");
                if (!out) { perror("output"); return 3; }
                unsigned char *copy = malloc(chunk);
                uint32_t written = 0;
                while (written < size) {
                    size_t n = size-written < chunk ? size-written : chunk;
                    ssize_t nread = pread(mem, copy, n, address+written);
                    if (nread <= 0) break;
                    fwrite(copy,1,nread,out); written += nread;
                }
                fclose(out); free(copy);
                printf("DEX address=%llx size=%u written=%u classes=%u file=%s\n",(unsigned long long)address,size,written,classes,name);
                fflush(stdout); count++;
            }
        }
    }
    printf("DONE candidates=%d\n",count);
    close(mem); fclose(maps); free(buf);
    return 0;
}
