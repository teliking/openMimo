#define _FILE_OFFSET_BITS 64
#define _GNU_SOURCE
#include <elf.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

int main(int argc,char **argv) {
    if(argc!=4){fprintf(stderr,"usage: %s PID MODULE OUTPUT\n",argv[0]);return 1;}
    char name[256],line[2048];
    snprintf(name,sizeof(name),"/proc/%d/maps",atoi(argv[1]));
    FILE *maps=fopen(name,"r");
    snprintf(name,sizeof(name),"/proc/%d/mem",atoi(argv[1]));
    int mem=open(name,O_RDONLY);
    if(!maps||mem<0){perror("process");return 2;}
    uint64_t base=0;
    while(fgets(line,sizeof(line),maps)) {
        unsigned long long start,end,offset;char prot[5];
        if(sscanf(line,"%llx-%llx %4s %llx",&start,&end,prot,&offset)==4 && offset==0 && strstr(line,argv[2])) {base=start;break;}
    }
    if(!base){fprintf(stderr,"module not mapped\n");return 3;}
    Elf64_Ehdr header;
    if(pread(mem,&header,sizeof(header),base)!=sizeof(header)||memcmp(header.e_ident,ELFMAG,SELFMAG)){fprintf(stderr,"invalid ELF\n");return 4;}
    Elf64_Phdr *phdrs=calloc(header.e_phnum,sizeof(Elf64_Phdr));
    pread(mem,phdrs,header.e_phnum*sizeof(Elf64_Phdr),base+header.e_phoff);
    FILE *out=fopen(argv[3],"wb");
    if(!out){perror("output");return 5;}
    char buffer[65536];
    for(int i=0;i<header.e_phnum;i++) {
        Elf64_Phdr *p=phdrs+i;
        if(p->p_type!=PT_LOAD)continue;
        printf("segment vaddr=%llx offset=%llx size=%llu\n",(unsigned long long)p->p_vaddr,(unsigned long long)p->p_offset,(unsigned long long)p->p_filesz);
        fseeko(out,p->p_offset,SEEK_SET);
        for(uint64_t done=0;done<p->p_filesz;){
            size_t want=p->p_filesz-done<sizeof(buffer)?p->p_filesz-done:sizeof(buffer);
            ssize_t got=pread(mem,buffer,want,base+p->p_vaddr+done);
            if(got<=0){perror("read memory");return 6;}
            fwrite(buffer,1,got,out);done+=got;
        }
    }
    fclose(out);free(phdrs);fclose(maps);close(mem);
    printf("base=%llx output=%s\n",(unsigned long long)base,argv[3]);return 0;
}
