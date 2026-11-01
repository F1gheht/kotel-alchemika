import json,sys,re
items=json.load(open(sys.argv[1]))['items']
fmt={x['UniqueName']:x.get('LocalizedNames') or {} for x in json.load(open(sys.argv[2]))}
def lst(x): return x if isinstance(x,list) else ([x] if x else [])
rec={}
def add(uid, r):
    ing={}; nr=[]
    for x in lst(r.get('craftresource')):
        ing[x['@uniquename']]=int(x['@count'])
        if x.get('@maxreturnamount')=='0': nr.append(x['@uniquename'])
    out={'n':int(r.get('@amountcrafted',1)),'focus':int(float(r.get('@craftingfocus',0))),'ing':ing}
    if nr: out['nr']=nr
    rec[uid]=out
for it in items['consumableitem']:
    u=it['@uniquename']
    if not re.match(r'^T\d_POTION_',u): continue
    r=lst(it.get('craftingrequirements'))
    if not r: continue
    add(u,r[0])
    for en in lst((it.get('enchantments') or {}).get('enchantment')):
        cr=lst(en.get('craftingrequirements'))
        if cr: add(f"{u}@{en['@enchantmentlevel']}", cr[0])
ids=set(rec)
for r in rec.values(): ids|=set(r['ing'])
names={i:{'ru':(fmt.get(i) or {}).get('RU-RU'),'en':(fmt.get(i) or {}).get('EN-US')} for i in ids}
print(len(rec), 'missing names', [i for i in ids if not names[i]['ru']][:10], file=sys.stderr)
print(rec.get('T8_POTION_LAVA@1'), names.get('T8_POTION_LAVA@1'), names.get('T1_ALCHEMY_EXTRACT_LEVEL1'), file=sys.stderr)
json.dump({'recipes':rec,'names':names},open(sys.argv[3],'w'),ensure_ascii=False)
